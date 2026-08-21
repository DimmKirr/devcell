package qemu

import (
	"fmt"
	"strings"
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
}

const (
	WimBuilderScriptName = `devcell-wim-builder.ps1`
	WimBuilderDoneFile   = `devcell-builder-done.txt`
	WimBuilderLogFile    = `devcell-builder.log`
)

// WimBuilderScriptCommand returns the agent command line for the builder.
// The agent runs this via Invoke-Expression in PowerShell, so $DevcellVol
// is expanded from the agent's scope.
func WimBuilderScriptCommand() string {
	return `& "$DevcellVol\` + WimBuilderScriptName + `" $DevcellVol`
}

// GenerateWimBuilderScript produces a PowerShell script that runs inside
// WinPE to service a boot.wim copy using DISM offline commands. The shared
// volume (passed as first argument) carries boot.wim in and devcell.wim out.
//
// Prerequisites in the VM:
//   - $args[0] (SHARED) has boot.wim
//   - A CD-ROM drive contains the Windows ISO with sources\install.wim
//
// On success the script writes devcell.wim and devcell-builder-done.txt to
// the shared volume.
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

	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Shared = $args[0]\r\n")
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DEVCELL WIM BUILDER ==='\r\n")
	b.WriteString("Write-Output \"$(Get-Date -Format o)\"\r\n")
	b.WriteString("Write-Output \"Shared volume: $Shared\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	failAndExit := func() {
		b.WriteString("    'FAIL' | Set-Content \"$Shared\\" + WimBuilderDoneFile + "\"\r\n")
		b.WriteString("    exit 1\r\n")
	}

	// Find the Windows ISO drive (contains sources\install.wim)
	if needsInstallWim {
		b.WriteString("$WinISO = $null\r\n")
		b.WriteString("foreach ($d in 'C','D','E','F','G','H','I','J','K','L','M','N','O','P','Q','R','S','T','U','V','W','Y','Z') {\r\n")
		b.WriteString("    if (Test-Path \"${d}:\\sources\\install.wim\") { $WinISO = \"${d}:\"; break }\r\n")
		b.WriteString("}\r\n")
		b.WriteString("if (-not $WinISO) {\r\n")
		b.WriteString("    Write-Output 'ERROR: Windows ISO not found'\r\n")
		failAndExit()
		b.WriteString("}\r\n")
		b.WriteString("Write-Output \"Found Windows ISO at $WinISO\"\r\n")
		b.WriteString("Write-Output ''\r\n")
	}

	// Find the virtio-win ISO drive
	if needsVirtIO {
		b.WriteString("$VirtIO = $null\r\n")
		b.WriteString("foreach ($d in 'C','D','E','F','G','H','I','J','K','L','M','N','O','P','Q','R','S','T','U','V','W','Y','Z') {\r\n")
		b.WriteString("    if (Test-Path \"${d}:\\vioserial\\w11\\ARM64\\vioser.inf\") { $VirtIO = \"${d}:\"; break }\r\n")
		b.WriteString("}\r\n")
		b.WriteString("if (-not $VirtIO) {\r\n")
		b.WriteString("    Write-Output 'ERROR: virtio-win ISO not found'\r\n")
		failAndExit()
		b.WriteString("}\r\n")
		b.WriteString("Write-Output \"Found virtio-win ISO at $VirtIO\"\r\n")
		b.WriteString("Write-Output ''\r\n")
	}

	// Verify source WIM exists on the shared volume
	fmt.Fprintf(&b, "if (-not (Test-Path \"$Shared\\%s\")) {\r\n", sourceWim)
	fmt.Fprintf(&b, "    Write-Output 'ERROR: %s not found on shared volume'\r\n", sourceWim)
	failAndExit()
	b.WriteString("}\r\n")
	fmt.Fprintf(&b, "Write-Output 'Found %s on shared volume'\r\n", sourceWim)
	b.WriteString("Write-Output ''\r\n")

	// Partition the scratch NVMe disk for DISM mount points.
	// Use W: (not C:) because the USB FAT shared volume may already hold C:
	// when it is the boot device (SCSI CD boot path with startup.nsh).
	b.WriteString("Write-Output '--- Preparing work volume (diskpart) ---'\r\n")
	b.WriteString("@'\r\n")
	b.WriteString("select disk 0\r\n")
	b.WriteString("clean\r\n")
	b.WriteString("create partition primary\r\n")
	b.WriteString("format fs=ntfs quick label=WORK\r\n")
	b.WriteString("assign letter=W\r\n")
	b.WriteString("'@ | Set-Content 'X:\\dp.txt' -Encoding ASCII\r\n")
	b.WriteString("& diskpart.exe /s X:\\dp.txt 2>&1 | Write-Output\r\n")
	b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
	b.WriteString("    Write-Output \"ERROR: diskpart failed with exit code $LASTEXITCODE\"\r\n")
	failAndExit()
	b.WriteString("}\r\n")
	b.WriteString("Write-Output 'Work volume W: ready'\r\n")
	b.WriteString("Write-Output ''\r\n")

	// Check internet connectivity
	b.WriteString("Write-Output '--- Checking internet connectivity ---'\r\n")
	b.WriteString("$HasInet = (Test-Connection -ComputerName dns.msftncsi.com -Count 1 -Quiet -ErrorAction SilentlyContinue)\r\n")
	b.WriteString("if ($HasInet) { Write-Output 'Internet: available' }\r\n")
	b.WriteString("else { Write-Output 'Internet: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// Create mount directories
	b.WriteString("Write-Output '--- Creating mount directories ---'\r\n")
	b.WriteString("New-Item -ItemType Directory -Force -Path 'W:\\mnt\\boot','W:\\mnt\\install' | Out-Null\r\n")
	b.WriteString("Write-Output ''\r\n")

	// Mount source WIM
	fmt.Fprintf(&b, "Write-Output '--- Mounting %s (index %d) ---'\r\n", sourceWim, idx)
	fmt.Fprintf(&b, "& dism.exe /Mount-Image /ImageFile:\"$Shared\\%s\" /Index:%d /MountDir:W:\\mnt\\boot 2>&1 | Write-Output\r\n", sourceWim, idx)
	b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
	fmt.Fprintf(&b, "    Write-Output 'ERROR: Failed to mount %s'\r\n", sourceWim)
	failAndExit()
	b.WriteString("}\r\n")
	fmt.Fprintf(&b, "Write-Output '%s mounted successfully'\r\n", sourceWim)
	b.WriteString("Write-Output ''\r\n")

	// Mount install.wim (read-only, index 1) when features/capabilities/packages need it as source.
	if needsInstallWim {
		b.WriteString("Write-Output '--- Mounting install.wim (index 1, read-only) ---'\r\n")
		b.WriteString("& dism.exe /Mount-Image /ImageFile:\"$WinISO\\sources\\install.wim\" /Index:1 /MountDir:W:\\mnt\\install /ReadOnly 2>&1 | Write-Output\r\n")
		b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
		b.WriteString("    Write-Output 'ERROR: Failed to mount install.wim'\r\n")
		b.WriteString("    & dism.exe /Unmount-Image /MountDir:W:\\mnt\\boot /Discard 2>&1 | Write-Output\r\n")
		failAndExit()
		b.WriteString("}\r\n")
		b.WriteString("Write-Output 'install.wim mounted successfully'\r\n")
		b.WriteString("Write-Output ''\r\n")
	}

	// Apply each operation
	b.WriteString("$OpsOK = 0\r\n")
	b.WriteString("$OpsFail = 0\r\n")
	b.WriteString("Write-Output '--- Applying servicing operations ---'\r\n")
	for i, op := range cfg.Ops {
		var cmd string
		var desc string
		switch {
		case op.Feature != "":
			desc = fmt.Sprintf("Enable-Feature %s", op.Feature)
			cmd = fmt.Sprintf("& dism.exe /Image:W:\\mnt\\boot /Enable-Feature /FeatureName:%s /All /Source:W:\\mnt\\install\\Windows /LimitAccess 2>&1 | Write-Output", op.Feature)
		case op.Package != "":
			desc = fmt.Sprintf("Add-Package %s", op.Package)
			cmd = fmt.Sprintf("& dism.exe /Image:W:\\mnt\\boot /Add-Package /PackagePath:W:\\mnt\\install\\%s 2>&1 | Write-Output", op.Package)
		case op.Driver != "":
			desc = fmt.Sprintf("Add-Driver %s", op.Driver)
			cmd = fmt.Sprintf("& dism.exe /Image:W:\\mnt\\boot /Add-Driver /Driver:\"$VirtIO\\%s\" /Recurse 2>&1 | Write-Output", op.Driver)
		case op.Capability != "":
			desc = fmt.Sprintf("Add-Capability %s", op.Capability)
			fmt.Fprintf(&b, "Write-Output '[%d/%d] %s'\r\n", i+1, len(cfg.Ops), desc)
			fmt.Fprintf(&b, "& dism.exe /Image:W:\\mnt\\boot /Add-Capability /CapabilityName:%s /Source:W:\\mnt\\install /LimitAccess 2>&1 | Write-Output\r\n", op.Capability)
			b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
			fmt.Fprintf(&b, "    Write-Output '%s failed offline'\r\n", desc)
			b.WriteString("    if ($HasInet) {\r\n")
			fmt.Fprintf(&b, "        Write-Output 'Retrying %s via Windows Update...'\r\n", desc)
			fmt.Fprintf(&b, "        & dism.exe /Image:W:\\mnt\\boot /Add-Capability /CapabilityName:%s 2>&1 | Write-Output\r\n", op.Capability)
			b.WriteString("        if ($LASTEXITCODE -ne 0) {\r\n")
			fmt.Fprintf(&b, "            Write-Output 'WARNING: %s failed via Windows Update'\r\n", desc)
			b.WriteString("            $OpsFail++\r\n")
			b.WriteString("        } else {\r\n")
			fmt.Fprintf(&b, "            Write-Output 'OK: %s (via Windows Update)'\r\n", desc)
			b.WriteString("            $OpsOK++\r\n")
			b.WriteString("        }\r\n")
			b.WriteString("    } else {\r\n")
			fmt.Fprintf(&b, "        Write-Output 'WARNING: %s failed and no internet'\r\n", desc)
			b.WriteString("        $OpsFail++\r\n")
			b.WriteString("    }\r\n")
			b.WriteString("} else {\r\n")
			fmt.Fprintf(&b, "    Write-Output 'OK: %s (offline)'\r\n", desc)
			b.WriteString("    $OpsOK++\r\n")
			b.WriteString("}\r\n")
			b.WriteString("Write-Output ''\r\n")
			continue
		default:
			continue
		}
		fmt.Fprintf(&b, "Write-Output '[%d/%d] %s'\r\n", i+1, len(cfg.Ops), desc)
		fmt.Fprintf(&b, "%s\r\n", cmd)
		b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
		fmt.Fprintf(&b, "    Write-Output 'WARNING: %s failed'\r\n", desc)
		b.WriteString("    $OpsFail++\r\n")
		b.WriteString("} else {\r\n")
		fmt.Fprintf(&b, "    Write-Output 'OK: %s'\r\n", desc)
		b.WriteString("    $OpsOK++\r\n")
		b.WriteString("}\r\n")
		b.WriteString("Write-Output ''\r\n")
	}
	b.WriteString("Write-Output \"Operations: $OpsOK succeeded, $OpsFail failed\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	// Verify: list enabled features and injected drivers
	b.WriteString("Write-Output '--- Verifying serviced image ---'\r\n")
	b.WriteString("& dism.exe /Image:W:\\mnt\\boot /Get-Features 2>&1 | Select-String -Pattern 'Enabled' -Context 1,0 | ForEach-Object { \"$($_.Context.PreContext[0].Trim()), $($_.Line.Trim())\" } | Write-Output\r\n")
	if needsVirtIO {
		b.WriteString("Write-Output ''\r\n")
		b.WriteString("Write-Output '--- Injected drivers ---'\r\n")
		b.WriteString("& dism.exe /Image:W:\\mnt\\boot /Get-Drivers 2>&1 | Select-String -Pattern 'oem' | Write-Output\r\n")
	}
	b.WriteString("Write-Output ''\r\n")

	// Unmount install.wim (discard, read-only)
	if needsInstallWim {
		b.WriteString("Write-Output '--- Unmounting install.wim ---'\r\n")
		b.WriteString("& dism.exe /Unmount-Image /MountDir:W:\\mnt\\install /Discard 2>&1 | Write-Output\r\n")
		b.WriteString("Write-Output ''\r\n")
	}

	// Write provenance marker into the mounted image root
	b.WriteString("Write-Output '--- Writing devcell info.json ---'\r\n")
	b.WriteString("'{\"version\":\"' + (Get-Date -Format o) + '\"}' | Set-Content 'W:\\mnt\\boot\\info.json'\r\n")
	b.WriteString("Write-Output ''\r\n")

	// Unmount source WIM (commit changes)
	fmt.Fprintf(&b, "Write-Output '--- Committing %s changes ---'\r\n", sourceWim)
	b.WriteString("& dism.exe /Unmount-Image /MountDir:W:\\mnt\\boot /Commit 2>&1 | Write-Output\r\n")
	b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
	fmt.Fprintf(&b, "    Write-Output 'ERROR: Failed to commit %s'\r\n", sourceWim)
	failAndExit()
	b.WriteString("}\r\n")
	fmt.Fprintf(&b, "Write-Output '%s committed successfully'\r\n", sourceWim)
	b.WriteString("Write-Output ''\r\n")

	// Copy to target WIM
	if sourceWim != targetWim {
		fmt.Fprintf(&b, "Write-Output '--- Creating %s ---'\r\n", targetWim)
		fmt.Fprintf(&b, "Copy-Item \"$Shared\\%s\" \"$Shared\\%s\" -Force\r\n", sourceWim, targetWim)
		b.WriteString("if (-not $?) {\r\n")
		fmt.Fprintf(&b, "    Write-Output 'ERROR: Failed to create %s'\r\n", targetWim)
		failAndExit()
		b.WriteString("}\r\n")
		fmt.Fprintf(&b, "Write-Output '%s created'\r\n", targetWim)
		b.WriteString("Write-Output ''\r\n")
	}

	// Success marker
	b.WriteString("Write-Output '=== DEVCELL WIM BUILDER COMPLETE ==='\r\n")
	b.WriteString("Write-Output \"Operations: $OpsOK succeeded, $OpsFail failed\"\r\n")
	b.WriteString("'SUCCESS' | Set-Content \"$Shared\\" + WimBuilderDoneFile + "\"\r\n")
	b.WriteString("Write-Output \"$(Get-Date -Format o)\"\r\n")

	return []byte(b.String())
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
		"/" + AgentVolumeMarker:    []byte("1"),
		"/" + AgentCommandFile:     []byte(WimBuilderScriptCommand()),
		"/" + WimBuilderScriptName: GenerateWimBuilderScript(cfg),
	}
	if len(efiBootLoader) > 0 {
		files["/startup.nsh"] = padForFAT([]byte(startupNSH))
		files["/EFI/BOOT/BOOTAA64.EFI"] = efiBootLoader
	}
	for path, data := range pwshFiles {
		files[path] = data
	}
	return files
}
