package qemu

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

	// Every exit path must emit the completion token so the host can
	// detect builder termination via the progress port.
	count := strings.Count(script, WimBuilderCompleteToken)
	assert.GreaterOrEqual(t, count, 2, "completion token must appear on both success and failure paths")

	assert.Contains(t, script, `W:\scratch`, "DISM scratch directory must use work volume")
	assert.Contains(t, script, "/ScratchDir:", "all DISM calls must use /ScratchDir")
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

func TestGenerateWimBuilderScript_InstallWimUnmountSkippedByDefault(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "Mounting install.wim", "install.wim must still be mounted")
	assert.NotContains(t, script, "Unmounting install.wim", "unmount must be skipped by default")
	assert.Contains(t, script, `status = 'skipped'`, "skipped status must be emitted")
	assert.Contains(t, script, "unmount skipped", "comment explaining skip must be present")
}

func TestGenerateWimBuilderScript_InstallWimUnmountExplicit(t *testing.T) {
	cfg := WimPrepConfig{
		Ops:               HyperVPrepOps(),
		UnmountInstallWim: true,
	}
	script := string(GenerateWimBuilderScript(cfg))

	assert.Contains(t, script, "Unmounting install.wim")
	assert.Contains(t, script, "/Unmount-Image /MountDir:W:\\mnt\\install /Discard")
	assert.NotContains(t, script, `status = 'skipped'`)
}

func TestGenerateWimBuilderScript_FeatureDiscovery(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// The template must discover backing packages for each Feature op
	// by running Get-FeatureInfo on install.wim BEFORE Enable-Feature.
	assert.Contains(t, script, "/Get-FeatureInfo /FeatureName:Microsoft-Hyper-V",
		"must discover backing packages for Microsoft-Hyper-V")
	assert.Contains(t, script, "/Get-FeatureInfo /FeatureName:VirtualMachinePlatform",
		"must discover backing packages for VirtualMachinePlatform")

	// Discovery must target install.wim (the source), not boot.wim.
	discoveryIdx := strings.Index(script, "/Get-FeatureInfo")
	assert.Greater(t, discoveryIdx, 0)
	assert.Contains(t, script, "/Image:W:\\mnt\\install /Get-FeatureInfo",
		"discovery must target install.wim mount")
}

func TestGenerateWimBuilderScript_FeaturePackageImport(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// After discovering packages, must import them into boot.wim
	// via Add-Package BEFORE running Enable-Feature.
	assert.Contains(t, script, "/Image:W:\\mnt\\boot /Add-Package",
		"must import discovered packages into boot.wim")

	// Package import must come BEFORE Enable-Feature in the script.
	importIdx := strings.Index(script, "/Add-Package")
	enableIdx := strings.Index(script, "/Enable-Feature")
	assert.Greater(t, importIdx, 0, "Add-Package must appear in script")
	assert.Greater(t, enableIdx, 0, "Enable-Feature must appear in script")
	assert.Less(t, importIdx, enableIdx,
		"Add-Package must come before Enable-Feature")
}

func TestGenerateWimBuilderScript_FeatureDiscoveryJSONL(t *testing.T) {
	cfg := WimPrepConfig{
		Ops: HyperVPrepOps(),
	}
	script := string(GenerateWimBuilderScript(cfg))

	// Must emit JSONL events for discovery and package import.
	assert.Contains(t, script, "event = 'feature_info'",
		"must emit feature_info JSONL event for discovery")
	assert.Contains(t, script, "event = 'add_package'",
		"must emit add_package JSONL event for package import")
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

func TestDismFilterParsesProgress(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not on PATH")
	}

	tmp := t.TempDir()
	jsonlPath := filepath.Join(tmp, "out.jsonl")

	// Minimal test harness: defines the filter with a file-backed structStream,
	// pipes sample DISM output through it, and writes JSON events to a file.
	testScript := `
$ErrorActionPreference = 'Continue'
$structStream = [System.IO.File]::Open('` + jsonlPath + `',
    [System.IO.FileMode]::Create,
    [System.IO.FileAccess]::Write,
    [System.IO.FileShare]::ReadWrite)

function devcell-ram { @{} }

function devcell-json([hashtable]$obj) {
    $out = [ordered]@{}
    if ($obj.ContainsKey('event')) { $out['event'] = $obj['event'] }
    if ($obj.ContainsKey('stage')) { $out['stage'] = $obj['stage'] }
    foreach ($k in ($obj.Keys | Sort-Object)) {
        if ($k -notin 'event','stage') { $out[$k] = $obj[$k] }
    }
    $line = ($out | ConvertTo-Json -Compress -Depth 3) + [char]10
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($line)
    $structStream.Write($bytes, 0, $bytes.Length)
    $structStream.Flush()
}

$script:dismLastPct = -1
$script:dismLastOp = ''
filter devcell-dism-out {
    param([string]$Op)
    if ($Op -ne $script:dismLastOp) {
        $script:dismLastPct = -1
        $script:dismLastOp = $Op
    }
    Write-Output $_
    if ($_ -match '\[.*?(\d+)\.0%') {
        $pct = [int]$Matches[1]
        if ($pct -ge ($script:dismLastPct + 10) -or $pct -eq 100) {
            devcell-json @{ event = 'dism_progress'; pct = $pct; op = $Op }
            $script:dismLastPct = $pct
        }
    } elseif ($_ -notmatch '^\s*$' -and $_ -notmatch '^\[') {
        devcell-json @{ event = 'dism_output'; line = $_.Trim(); op = $Op }
    }
}

# --- Test cases ---
# Simulate DISM mount output (progress + text)
@(
    'Deployment Image Servicing and Management tool',
    'Version: 10.0.26100.1',
    '',
    'Mounting image',
    '',
    '[=                          2.0%                           ]',
    '',
    '[=====                      10.0%                          ]',
    '',
    '[===========                20.0%                          ]',
    '',
    '[========================== 50.0%                          ]',
    '',
    '[===========================99.0%========================= ]',
    '',
    '[==========================100.0%==========================]',
    'The operation completed successfully.'
) | devcell-dism-out -Op 'mount-boot'

# Second operation should reset the counter
@(
    '[=                          5.0%                           ]',
    '',
    '[==========================100.0%==========================]',
    'The operation completed successfully.'
) | devcell-dism-out -Op 'unmount-install'

$structStream.Close()
`
	scriptPath := filepath.Join(tmp, "test-filter.ps1")
	require.NoError(t, os.WriteFile(scriptPath, []byte(testScript), 0644))

	out, err := exec.Command(pwsh, "-NoProfile", "-File", scriptPath).CombinedOutput()
	require.NoError(t, err, "pwsh failed:\n%s", out)

	jsonlData, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
	require.NotEmpty(t, lines, "no JSON events emitted")

	type dismEvent struct {
		Event string `json:"event"`
		Pct   int    `json:"pct"`
		Op    string `json:"op"`
		Line  string `json:"line"`
	}

	var events []dismEvent
	for _, line := range lines {
		var e dismEvent
		require.NoError(t, json.Unmarshal([]byte(line), &e), "bad JSON: %s", line)
		events = append(events, e)
	}

	byTypeOp := func(eventType, op string) []dismEvent {
		var out []dismEvent
		for _, e := range events {
			if e.Event == eventType && (op == "" || e.Op == op) {
				out = append(out, e)
			}
		}
		return out
	}

	// Verify text output events (non-blank, non-progress-bar lines)
	textEvents := byTypeOp("dism_output", "")
	assert.GreaterOrEqual(t, len(textEvents), 2, "should capture text lines like tool version")
	if len(textEvents) > 0 {
		assert.Equal(t, "mount-boot", textEvents[0].Op)
		assert.Contains(t, textEvents[0].Line, "Deployment Image Servicing")
	}

	// Verify progress events from mount-boot operation
	mountProgress := byTypeOp("dism_progress", "mount-boot")
	assert.GreaterOrEqual(t, len(mountProgress), 3, "should emit progress at 10%% intervals")

	var mountPcts []int
	for _, e := range mountProgress {
		mountPcts = append(mountPcts, e.Pct)
	}
	assert.Contains(t, mountPcts, 10)
	assert.Contains(t, mountPcts, 20)
	assert.Contains(t, mountPcts, 100)
	assert.NotContains(t, mountPcts, 2, "2%% should be skipped (below 10%% threshold)")

	// Verify second operation resets counter
	unmountProgress := byTypeOp("dism_progress", "unmount-install")
	assert.GreaterOrEqual(t, len(unmountProgress), 1, "second op should emit progress")
	if len(unmountProgress) > 0 {
		assert.Equal(t, 100, unmountProgress[len(unmountProgress)-1].Pct)
	}

	// Verify key ordering: event before other keys
	for _, line := range lines {
		keys := jsonKeyOrder(line)
		eventIdx := keyIndex(keys, "event")
		if eventIdx >= 0 {
			for _, late := range []string{"op", "pct", "line"} {
				lateIdx := keyIndex(keys, late)
				if lateIdx >= 0 {
					assert.Less(t, eventIdx, lateIdx,
						"'event' must come before '%s': %s", late, line)
				}
			}
		}
	}
}

func jsonKeyOrder(line string) []string {
	d := json.NewDecoder(strings.NewReader(line))
	d.Token() // opening {
	var keys []string
	for d.More() {
		tok, _ := d.Token()
		if k, ok := tok.(string); ok {
			keys = append(keys, k)
			var v json.RawMessage
			d.Decode(&v)
		}
	}
	return keys
}

func keyIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
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

// The VMP transplant is host-side work, not a DISM op: DISM cannot enable
// VirtualMachinePlatform in a WinPE image at all (CBS parent-package gate).
func TestWimPrepConfig_TransplantVMPDefaultsOff(t *testing.T) {
	var cfg WimPrepConfig
	assert.False(t, cfg.TransplantVMP,
		"transplant must be opt-in so existing pipelines are unaffected")
}

func TestWimPrepConfig_TransplantVMPIsNotADismOp(t *testing.T) {
	cfg := WimPrepConfig{Ops: VirtIODriverPrepOps(), TransplantVMP: true}
	script := string(GenerateWimBuilderScript(cfg))

	assert.NotContains(t, script, "VirtualMachinePlatform",
		"the transplant must not emit DISM feature commands into the builder script")
}
