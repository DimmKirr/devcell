package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateWimBuilderScript_ContainsAllOps(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: append(HyperVPrepOps(), OpenSSHPrepOps()...),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// Must contain DISM commands for each feature/capability.
	assert.Contains(t, script, "Microsoft-Hyper-V")
	assert.Contains(t, script, "VirtualMachinePlatform")
	assert.Contains(t, script, "OpenSSH.Server~~~~0.0.1.0")
	assert.Contains(t, script, "OpenSSH.Client~~~~0.0.1.0")

	// Feature ops use /Enable-Feature, capability ops use /Add-Capability.
	assert.Contains(t, script, "/Enable-Feature /FeatureName:Microsoft-Hyper-V")
	assert.Contains(t, script, "/Add-Capability /CapabilityName:OpenSSH.Server")

	// Must use offline servicing (/Image:) not /Online.
	assert.NotContains(t, script, "/Online")
	assert.Contains(t, script, "/Image:W:\\mnt\\boot")

	// Must reference install.wim as the source.
	assert.Contains(t, script, "/Source:W:\\mnt\\install")

	// Must produce devcell.wim output.
	assert.Contains(t, script, "devcell.wim")

	// Must write success/fail marker.
	assert.Contains(t, script, WimBuilderDoneFile)
}

func TestGenerateWimBuilderScript_DefaultImageIndex(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Feature: "TestFeature"}},
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Index:2")
}

func TestGenerateWimBuilderScript_CustomImageIndex(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:           []WimPrepOp{{Feature: "TestFeature"}},
		WimImageIndex: 1,
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Index:1")
}

func TestGenerateWimBuilderScript_PackageOp(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{
			{Package: `Windows\WinSxS\Manifests\amd64_some_package.mum`},
		},
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "/Add-Package /PackagePath:W:\\mnt\\install\\")
	assert.Contains(t, script, "amd64_some_package.mum")
}

func TestGenerateWimBuilderScript_CRLFLineEndings(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		require.True(t, strings.HasSuffix(line, "\r"),
			"line %d must end with \\r\\n: %q", i+1, line)
	}
}

func TestGenerateWimBuilderScript_ErrorHandling(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "sources\\install.wim")
	assert.Contains(t, script, "Windows ISO not found")
	assert.Contains(t, script, "boot.wim not found")
	assert.Contains(t, script, "Failed to mount boot.wim")
	assert.Contains(t, script, "Failed to commit boot.wim")
}

func TestGenerateWimBuilderScript_InternetCheckAndCapabilityRetry(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: OpenSSHPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "Checking internet connectivity")
	assert.Contains(t, script, "Test-Connection")
	assert.Contains(t, script, "$HasInet")

	assert.Contains(t, script, "/LimitAccess")
	assert.Contains(t, script, "failed offline")
	assert.Contains(t, script, "Retrying")
	assert.Contains(t, script, "via Windows Update")
	assert.Contains(t, script, "no internet")
}

func TestGenerateWimBuilderScript_DiskpartWorkVolume(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "diskpart.exe /s")
	assert.Contains(t, script, "format fs=ntfs quick")
	assert.Contains(t, script, "assign letter=W")
	assert.Contains(t, script, "diskpart failed")
}

func TestHyperVPrepOps(t *testing.T) {
	ops := HyperVPrepOps()
	require.Len(t, ops, 2)
	assert.Equal(t, "Microsoft-Hyper-V", ops[0].Feature)
	assert.Equal(t, "VirtualMachinePlatform", ops[1].Feature)
}

func TestWSL2PrepOps(t *testing.T) {
	ops := WSL2PrepOps()
	require.Len(t, ops, 1)
	assert.Equal(t, "Microsoft-Windows-Subsystem-Linux", ops[0].Feature)
}

func TestGenerateWimBuilderScript_WSL2Feature(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: append(HyperVPrepOps(), WSL2PrepOps()...),
	}
	script := string(GenerateWimBuilderScript(cfg))
	assert.Contains(t, script, "Microsoft-Windows-Subsystem-Linux")
	assert.Contains(t, script, "/Enable-Feature /FeatureName:Microsoft-Windows-Subsystem-Linux")
}

func TestOpenSSHPrepOps(t *testing.T) {
	ops := OpenSSHPrepOps()
	require.Len(t, ops, 2)
	assert.Equal(t, "OpenSSH.Server~~~~0.0.1.0", ops[0].Capability)
	assert.Equal(t, "OpenSSH.Client~~~~0.0.1.0", ops[1].Capability)
}

func TestGenerateWimBuilderScript_DefaultSourceAndTarget(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Feature: "TestFeature"}},
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, `$Shared\boot.wim`)
	assert.Contains(t, script, `$Shared\devcell.wim`)
}

func TestGenerateWimBuilderScript_CustomSourceWim(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:       []WimPrepOp{{Feature: "TestFeature"}},
		SourceWim: "install.wim",
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, `$Shared\install.wim`)
	assert.Contains(t, script, "install.wim not found")
	assert.NotContains(t, script, `$Shared\boot.wim`)
}

func TestGenerateWimBuilderScript_CustomTargetWim(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:       []WimPrepOp{{Feature: "TestFeature"}},
		TargetWim: "custom-output.wim",
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, `custom-output.wim`)
	assert.Contains(t, script, `$Shared\boot.wim`)
}

func TestGenerateWimBuilderScript_SameSourceAndTarget_NoCopy(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:       []WimPrepOp{{Feature: "TestFeature"}},
		SourceWim: "devcell.wim",
		TargetWim: "devcell.wim",
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.NotContains(t, script, "Copy-Item \"$Shared\\devcell.wim\"")
}

func TestVirtIODriverPrepOps(t *testing.T) {
	ops := VirtIODriverPrepOps()
	require.Len(t, ops, 3)
	assert.Equal(t, `NetKVM\w11\ARM64`, ops[0].Driver)
	assert.Equal(t, `vioserial\w11\ARM64`, ops[1].Driver)
	assert.Equal(t, `vioscsi\w11\ARM64`, ops[2].Driver)
}

func TestGenerateWimBuilderScript_DriverOp(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Driver: `NetKVM\w11\ARM64`}},
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, `/Add-Driver /Driver:"$VirtIO\NetKVM\w11\ARM64" /Recurse`)
	assert.Contains(t, script, "/Image:W:\\mnt\\boot")
}

func TestGenerateWimBuilderScript_DriverOpProbesVirtioISO(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Driver: `NetKVM\w11\ARM64`}},
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "$VirtIO = $null")
	assert.Contains(t, script, `vioserial\w11\ARM64\vioser.inf`)
	assert.Contains(t, script, "virtio-win ISO not found")
}

func TestGenerateWimBuilderScript_NoDriverOp_NoVirtioProbe(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Feature: "TestFeature"}},
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.NotContains(t, script, "$VirtIO = $null")
	assert.NotContains(t, script, "virtio-win ISO not found")
}

func TestGenerateWimBuilderScript_DriversOnly_NoInstallWimMount(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: VirtIODriverPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.NotContains(t, script, "Mounting install.wim")
	assert.NotContains(t, script, "Unmounting install.wim")
	assert.NotContains(t, script, "Windows ISO not found")
	assert.Contains(t, script, "Mounting boot.wim")
	assert.Contains(t, script, "Committing boot.wim")
}

func TestGenerateWimBuilderScript_DriverOpVerifiesDrivers(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: []WimPrepOp{{Driver: `NetKVM\w11\ARM64`}},
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "/Get-Drivers")
}

func TestGenerateWimBuilderScript_MixedOps(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: append(HyperVPrepOps(), VirtIODriverPrepOps()...),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "/Enable-Feature /FeatureName:Microsoft-Hyper-V")
	assert.Contains(t, script, `/Add-Driver /Driver:"$VirtIO\NetKVM\w11\ARM64" /Recurse`)
	assert.Contains(t, script, `/Add-Driver /Driver:"$VirtIO\vioserial\w11\ARM64" /Recurse`)
	assert.Contains(t, script, `/Add-Driver /Driver:"$VirtIO\vioscsi\w11\ARM64" /Recurse`)
	assert.Contains(t, script, "$VirtIO = $null")
}

func TestBuildWimBuilderArgv_VirtIOISO(t *testing.T) {
	s := testSpec()
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=cdrom2")
	assert.Contains(t, joined, "usb-storage,drive=cdrom2")
}

func TestBuildWimBuilderArgv_NoVirtIOISO(t *testing.T) {
	s := testSpec()
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "cdrom2")
}

func TestBuildWimBuilderArgv_SCSI(t *testing.T) {
	s := testSpec()
	s.CDBus = "scsi"
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "virtio-scsi-pci,id="+CDBusID,
		"SCSI bus controller must be present")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom0",
		"WinPE ISO must be on scsi-cd")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom1",
		"Windows ISO must be on scsi-cd")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom2",
		"VirtIO ISO must be on scsi-cd")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0,bootindex=2",
		"shared FAT volume must be on usb-storage with bootindex=2 for startup.nsh chainload")
	assert.NotContains(t, joined, "usb-storage,drive=cdrom",
		"ISOs must not be on usb-storage in SCSI mode")
}

func TestWimBuilderScriptCommand(t *testing.T) {
	cmd := WimBuilderScriptCommand()
	assert.Contains(t, cmd, WimBuilderScriptName)
	assert.Contains(t, cmd, "$DevcellVol")
}
