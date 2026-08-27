package qemu

import (
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/templates"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"
)

// WimPrepOp describes a single DISM offline servicing operation to apply to
// a WIM image. The builder WinPE script iterates these in order.
type WimPrepOp struct {
	// Feature enables a Windows feature via
	//   dism /Image:<mount> /Enable-Feature /FeatureName:<Feature> /All
	//     /Source:<install.wim-mount>\Windows /LimitAccess
	// Mutually exclusive with Package, Capability, and Driver.
	Feature string

	// Package adds a .cab or .mum package via
	//   dism /Image:<mount> /Add-Package /PackagePath:<Package>
	// The path is relative to the install.wim mount point.
	Package string

	// Capability adds a Windows capability via
	//   dism /Image:<mount> /Add-Capability /CapabilityName:<Capability>
	//     /Source:<install.wim-mount>
	Capability string

	// Driver injects a driver via
	//   dism /Image:<mount> /Add-Driver /Driver:%VIRTIO%\<Driver> /Recurse
	// The path is relative to the virtio-win ISO root (e.g. "NetKVM\w11\ARM64").
	// The script probes for the virtio-win drive letter automatically when
	// any op uses this field.
	Driver string
}

// WimPrepConfig parameterises the WIM builder pipeline. A builder WinPE boots,
// mounts boot.wim and install.wim, applies the listed operations via DISM
// offline servicing, and writes the result as devcell.wim.
type WimPrepConfig struct {
	// Ops is the ordered list of servicing operations.
	Ops []WimPrepOp

	// WimImageIndex is the boot.wim image to service (default 2 =
	// "Microsoft Windows Setup").
	WimImageIndex int

	// SourceWim is the filename of the WIM to service on the shared
	// volume. Default: "boot.wim".
	SourceWim string

	// TargetWim is the filename of the output WIM on the shared volume.
	// When equal to SourceWim the copy step is skipped (DISM commits
	// in place). Default: "devcell.wim".
	TargetWim string

	// UnmountInstallWim controls whether the builder explicitly unmounts
	// install.wim before committing boot.wim. Default false: the mount is
	// read-only and the WinPE VM is discarded after the build, so an
	// explicit unmount just burns wall-clock (15 min on TCG/ARM64).
	UnmountInstallWim bool

	// TransplantVMP requests the VirtualMachinePlatform transplant on the
	// host, before the builder VM boots. It produces no DISM commands:
	// DISM cannot enable VMP in a WinPE image at all, because every backing
	// package names Microsoft-Windows-Foundation-Package as its parent while
	// boot.wim's is Microsoft-Windows-WinPE-Package, so CBS refuses both
	// /Add-Package and /Enable-Feature. The transplant copies the signed
	// binaries in and clones the service keys instead.
	TransplantVMP bool
}

const (
	WimBuilderScriptName    = `devcell-wim-builder.ps1`
	WimBuilderDoneFile      = `devcell-builder-done.txt`
	WimBuilderLogFile       = `devcell-builder.log`
	WimBuilderCompleteToken = `DEVCELL_BUILDER_DONE`
)

// WimBuilderScriptCommand returns the agent command line for the builder.
// The agent runs this via Invoke-Expression in PowerShell, so $DevcellVol
// is expanded from the agent's scope.
func WimBuilderScriptCommand() string {
	return `& "$DevcellVol\` + WimBuilderScriptName + `" $DevcellVol`
}

type wimBuilderOp struct {
	Feature    string
	Capability string
	Package    string
	Driver     string
	Num        int
	Total      int
}

type wimBuilderData struct {
	StructuredPortName string
	NeedsInstallWim    bool
	UnmountInstallWim  bool
	NeedsVirtIO        bool
	SourceWim          string
	TargetWim          string
	WimIndex           int
	DoneFile           string
	CompleteToken      string
	Ops                []wimBuilderOp
}

// GenerateWimBuilderScript produces a PowerShell script that runs inside
// WinPE to service a boot.wim copy using DISM offline commands. The shared
// volume (passed as first argument) carries boot.wim in and devcell.wim out.
func GenerateWimBuilderScript(cfg WimPrepConfig) []byte {
	idx := cfg.WimImageIndex
	if idx == 0 {
		idx = 2
	}
	sourceWim := cfg.SourceWim
	if sourceWim == "" {
		sourceWim = "boot.wim"
	}
	targetWim := cfg.TargetWim
	if targetWim == "" {
		targetWim = "devcell.wim"
	}

	needsVirtIO := false
	needsInstallWim := false
	for _, op := range cfg.Ops {
		if op.Driver != "" {
			needsVirtIO = true
		}
		if op.Feature != "" || op.Capability != "" || op.Package != "" {
			needsInstallWim = true
		}
	}

	total := len(cfg.Ops)
	ops := make([]wimBuilderOp, 0, total)
	for i, op := range cfg.Ops {
		ops = append(ops, wimBuilderOp{
			Feature:    op.Feature,
			Capability: op.Capability,
			Package:    op.Package,
			Driver:     op.Driver,
			Num:        i + 1,
			Total:      total,
		})
	}

	data := wimBuilderData{
		StructuredPortName: StructuredPortName,
		NeedsInstallWim:    needsInstallWim,
		UnmountInstallWim:  cfg.UnmountInstallWim,
		NeedsVirtIO:        needsVirtIO,
		SourceWim:          sourceWim,
		TargetWim:          targetWim,
		WimIndex:           idx,
		DoneFile:           WimBuilderDoneFile,
		CompleteToken:      WimBuilderCompleteToken,
		Ops:                ops,
	}

	out := templates.Render("wim-builder.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}

// HyperVPrepOps returns the servicing operations to enable Hyper-V in a
// boot.wim. This is the minimum set needed for vmms.exe, vmwp.exe,
// vmcompute.exe, Vid.sys, and the full hypervisor host stack.
func HyperVPrepOps() []WimPrepOp {
	return []WimPrepOp{
		{Feature: "Microsoft-Hyper-V"},
		{Feature: "VirtualMachinePlatform"},
	}
}

// WSL2PrepOps returns the servicing operations to enable WSL2 in a boot.wim.
// Requires Hyper-V (HyperVPrepOps) as a prerequisite — VirtualMachinePlatform
// is included there.
func WSL2PrepOps() []WimPrepOp {
	return []WimPrepOp{
		{Feature: "Microsoft-Windows-Subsystem-Linux"},
	}
}

// OpenSSHPrepOps returns the servicing operations to add OpenSSH to a
// boot.wim. Uses the capability name rather than a feature.
func OpenSSHPrepOps() []WimPrepOp {
	return []WimPrepOp{
		{Capability: "OpenSSH.Server~~~~0.0.1.0"},
		{Capability: "OpenSSH.Client~~~~0.0.1.0"},
	}
}

// VirtIODriverPrepOps returns the servicing operations to inject the ARM64
// virtio-win drivers (NetKVM, vioserial, vioscsi) into a WIM image. The
// paths are relative to the virtio-win ISO root.
func VirtIODriverPrepOps() []WimPrepOp {
	return []WimPrepOp{
		{Driver: `NetKVM\w11\ARM64`},
		{Driver: `vioserial\w11\ARM64`},
		{Driver: `vioscsi\w11\ARM64`},
	}
}

// WimBuilderResult holds the output of a successful WIM builder run.
type WimBuilderResult struct {
	// SharedImg is the path to the shared FAT volume after the builder
	// finished. Contains devcell.wim and the builder log.
	SharedImg string
	// AgentOutput is the agent's combined stdout/stderr (if available).
	AgentOutput string
}

// WimBuilderSpec configures a WIM builder WinPE boot. Callers populate this
// and pass it to BuildWimBuilderArgv to get a QEMU command line.
type WimBuilderSpec struct {
	// Spec is the base QEMU spec (machine, firmware, etc.).
	Spec Spec
	// WinPEISO is the bootable WinPE ISO (may be stock or custom-injected).
	WinPEISO string
	// SharedImg is the FAT volume with boot.wim + builder script.
	SharedImg string
	// WindowsISO is the full Windows ISO (provides install.wim).
	WindowsISO string
	// VirtIOISO is the virtio-win ISO (provides driver directories for
	// DISM /Add-Driver). Required when any WimPrepOp uses the Driver field.
	VirtIOISO string
	// EFIBootLoader is the raw bytes of BOOTAA64.EFI. When set and CDBus
	// is "scsi", SharedVolumeFiles ships it at /EFI/BOOT/BOOTAA64.EFI
	// alongside startup.nsh so the firmware can chainload WinPE from the
	// FAT volume after failing to read ISO9660 on the SCSI CD.
	EFIBootLoader []byte
}

// BuildWimBuilderArgv constructs the QEMU argv for the WIM builder VM.
// When Spec.CDBus is "scsi", ISOs are attached on a virtio-scsi-pci
// controller (scsi-cd) so EDK2 can boot them on QEMU 11.x/HVF where
// USB-attached ISOs are invisible to the firmware. The shared FAT
// volume always goes on usb-storage (Windows needs it as removable media).
func BuildWimBuilderArgv(wbs WimBuilderSpec) []string {
	if wbs.Spec.CDBus == "scsi" {
		return buildWimBuilderSCSI(wbs)
	}
	return buildWimBuilderUSB(wbs)
}

func buildWimBuilderUSB(wbs WimBuilderSpec) []string {
	argv := BuildWinPECommand(wbs.Spec, wbs.WinPEISO, wbs.SharedImg)

	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.WindowsISO+",media=cdrom,if=none,id=cdrom1",
			"-device", "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
	}
	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.VirtIOISO+",media=cdrom,if=none,id=cdrom2",
			"-device", "usb-storage,drive=cdrom2,removable=true,bus="+USBBusID+".0")
	}
	return argv
}

func buildWimBuilderSCSI(wbs WimBuilderSpec) []string {
	argv := baseCommand(wbs.Spec)

	argv = append(argv, "-device", fmt.Sprintf("virtio-scsi-pci,id=%s", CDBusID))

	// WinPE ISO on scsi-cd (boot device)
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", wbs.WinPEISO),
		"-device", fmt.Sprintf("scsi-cd,drive=cdrom0,bus=%s.0,id=%s,bootindex=1",
			CDBusID, InstallerCDDeviceID))

	// Shared FAT volume on usb-storage (WinPE reads/writes it).
	// bootindex=2: EDK2 can't read ISO9660 on SCSI CDs, so the CD boot
	// (bootindex=1) fails. The firmware then tries the FAT volume, finds
	// startup.nsh, and chainloads BOOTAA64.EFI which loads boot.wim.
	if wbs.SharedImg != "" {
		driveFormat := "raw"
		if strings.HasSuffix(wbs.SharedImg, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=usbfat0", wbs.SharedImg, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=2", USBBusID))
	}

	// Windows ISO on scsi-cd (provides install.wim)
	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom1", wbs.WindowsISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom1,bus=%s.0", CDBusID))
	}

	// VirtIO ISO on scsi-cd (provides driver directories)
	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom2", wbs.VirtIOISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom2,bus=%s.0", CDBusID))
	}

	return argv
}

// SharedVolumeFiles returns the files to place on the builder's shared FAT
// volume. The caller must also add "/boot.wim" with the actual boot.wim
// content — it's excluded here because it's large and already on disk.
//
// pwshFiles carries the extracted PowerShell 7 directory (from ExtractPwshFiles).
// Stock WinPE lacks powershell.exe, so the bootstrap.cmd shim probes for
// pwsh.exe on the volume at runtime.
//
// When efiBootLoader is non-nil, the volume also ships startup.nsh and
// /EFI/BOOT/BOOTAA64.EFI. EDK2 pflash has no ISO9660 driver, so SCSI CDs
// appear as BLK-only. The FAT volume (usb-storage) gets a bootindex and
// startup.nsh chainloads the Windows Boot Manager, which loads boot.wim.
func SharedVolumeFiles(cfg WimPrepConfig, efiBootLoader []byte, pwshFiles map[string][]byte) map[string][]byte {
	files := map[string][]byte{
		"/" + winpe.AgentVolumeMarker: []byte("1"),
		"/" + winpe.AgentCommandFile:  []byte(WimBuilderScriptCommand()),
		"/" + WimBuilderScriptName:    GenerateWimBuilderScript(cfg),
	}
	if len(efiBootLoader) > 0 {
		files["/startup.nsh"] = unattend.PadForFAT([]byte(unattend.StartupNSH))
		files["/EFI/BOOT/BOOTAA64.EFI"] = efiBootLoader
	}
	for path, data := range pwshFiles {
		files[path] = data
	}
	return files
}
