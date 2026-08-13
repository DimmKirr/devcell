package qemu

import (
	"fmt"
	"strings"
)

// WimPrepOp describes a single DISM offline servicing operation to apply to
// a boot.wim image. The builder WinPE script iterates these in order.
type WimPrepOp struct {
	// Feature enables a Windows feature via
	//   dism /Image:<mount> /Enable-Feature /FeatureName:<Feature> /All
	//     /Source:<install.wim-mount>\Windows /LimitAccess
	// Mutually exclusive with Package and Capability.
	Feature string

	// Package adds a .cab or .mum package via
	//   dism /Image:<mount> /Add-Package /PackagePath:<Package>
	// The path is relative to the install.wim mount point.
	Package string

	// Capability adds a Windows capability via
	//   dism /Image:<mount> /Add-Capability /CapabilityName:<Capability>
	//     /Source:<install.wim-mount>
	Capability string
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
}

const (
	WimBuilderScriptName = `devcell-wim-builder.cmd`
	WimBuilderDoneFile   = `devcell-builder-done.txt`
	WimBuilderLogFile    = `devcell-builder.log`
)

// WimBuilderScriptCommand returns the agent command line for the builder.
func WimBuilderScriptCommand() string {
	return `%DEVCELL_VOL%\` + WimBuilderScriptName + ` %DEVCELL_VOL%`
}

// GenerateWimBuilderScript produces a batch script that runs inside WinPE to
// service a boot.wim copy using DISM offline commands. The shared volume
// (passed as %1) carries boot.wim in and devcell.wim out.
//
// Prerequisites in the VM:
//   - %1 (SHARED) has boot.wim
//   - A CD-ROM drive contains the Windows ISO with sources\install.wim
//
// On success the script writes devcell.wim and devcell-builder-done.txt to
// the shared volume.
func GenerateWimBuilderScript(cfg WimPrepConfig) []byte {
	idx := cfg.WimImageIndex
	if idx == 0 {
		idx = 2
	}

	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal enabledelayedexpansion\r\n")
	b.WriteString("set SHARED=%1\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo === DEVCELL WIM BUILDER ===\r\n")
	b.WriteString("echo %DATE% %TIME%\r\n")
	b.WriteString("echo Shared volume: %SHARED%\r\n")
	b.WriteString("echo.\r\n")

	// Find the Windows ISO drive (contains sources\install.wim)
	b.WriteString("set WINISO=\r\n")
	b.WriteString("for %%d in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do (\r\n")
	b.WriteString("    if exist %%d:\\sources\\install.wim set WINISO=%%d:\r\n")
	b.WriteString(")\r\n")
	b.WriteString("if \"%WINISO%\"==\"\" (\r\n")
	b.WriteString("    echo ERROR: Windows ISO not found — no drive has sources\\install.wim\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo Found Windows ISO at %WINISO%\r\n")
	b.WriteString("echo.\r\n")

	// Verify boot.wim exists on the shared volume
	b.WriteString("if not exist %SHARED%\\boot.wim (\r\n")
	b.WriteString("    echo ERROR: boot.wim not found on shared volume %SHARED%\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo Found boot.wim on %SHARED%\r\n")
	b.WriteString("echo.\r\n")

	// WinPE boots from X: (RAM disk) — there is no C: drive. Partition the
	// scratch disk to create a work volume for DISM mount points and scratch
	// space. diskpart assigns C: to the first created volume.
	b.WriteString("echo --- Preparing work volume (diskpart) ---\r\n")
	b.WriteString("echo select disk 0 > X:\\dp.txt\r\n")
	b.WriteString("echo clean >> X:\\dp.txt\r\n")
	b.WriteString("echo create partition primary >> X:\\dp.txt\r\n")
	b.WriteString("echo format fs=ntfs quick label=WORK >> X:\\dp.txt\r\n")
	b.WriteString("echo assign letter=C >> X:\\dp.txt\r\n")
	b.WriteString("diskpart /s X:\\dp.txt 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo ERROR: diskpart failed — exit code !ERRORLEVEL!\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo Work volume C: ready\r\n")
	b.WriteString("echo.\r\n")

	// Check internet connectivity — capabilities like OpenSSH.Server are
	// "Staged with no payload" in install.wim, so DISM needs Windows Update.
	// Features (Hyper-V) have full payloads in WinSxS and work offline.
	b.WriteString("echo --- Checking internet connectivity ---\r\n")
	b.WriteString("set HAS_INET=0\r\n")
	b.WriteString("ping -n 1 -w 3000 dns.msftncsi.com >nul 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! equ 0 (\r\n")
	b.WriteString("    set HAS_INET=1\r\n")
	b.WriteString("    echo Internet: available\r\n")
	b.WriteString(") else (\r\n")
	b.WriteString("    echo Internet: not available — capabilities that need Windows Update will be skipped\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// Create mount directories on the work volume
	b.WriteString("echo --- Creating mount directories ---\r\n")
	b.WriteString("mkdir C:\\mnt\\boot 2>nul\r\n")
	b.WriteString("mkdir C:\\mnt\\install 2>nul\r\n")
	b.WriteString("echo.\r\n")

	// Mount boot.wim
	fmt.Fprintf(&b, "echo --- Mounting boot.wim (index %d) ---\r\n", idx)
	fmt.Fprintf(&b, "dism /Mount-Image /ImageFile:%%SHARED%%\\boot.wim /Index:%d /MountDir:C:\\mnt\\boot 2>&1\r\n", idx)
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo ERROR: Failed to mount boot.wim — exit code !ERRORLEVEL!\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo boot.wim mounted successfully\r\n")
	b.WriteString("echo.\r\n")

	// Mount install.wim (read-only, index 1)
	b.WriteString("echo --- Mounting install.wim (index 1, read-only) ---\r\n")
	b.WriteString("dism /Mount-Image /ImageFile:%WINISO%\\sources\\install.wim /Index:1 /MountDir:C:\\mnt\\install /ReadOnly 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo ERROR: Failed to mount install.wim — exit code !ERRORLEVEL!\r\n")
	b.WriteString("    dism /Unmount-Image /MountDir:C:\\mnt\\boot /Discard 2>&1\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo install.wim mounted successfully\r\n")
	b.WriteString("echo.\r\n")

	// Apply each operation
	b.WriteString("set OPS_OK=0\r\n")
	b.WriteString("set OPS_FAIL=0\r\n")
	b.WriteString("echo --- Applying servicing operations ---\r\n")
	for i, op := range cfg.Ops {
		var cmd string
		var desc string
		switch {
		case op.Feature != "":
			desc = fmt.Sprintf("Enable-Feature %s", op.Feature)
			cmd = fmt.Sprintf("dism /Image:C:\\mnt\\boot /Enable-Feature /FeatureName:%s /All /Source:C:\\mnt\\install\\Windows /LimitAccess", op.Feature)
		case op.Package != "":
			desc = fmt.Sprintf("Add-Package %s", op.Package)
			cmd = fmt.Sprintf("dism /Image:C:\\mnt\\boot /Add-Package /PackagePath:C:\\mnt\\install\\%s", op.Package)
		case op.Capability != "":
			desc = fmt.Sprintf("Add-Capability %s", op.Capability)
			// Capabilities are often Staged with no payload in install.wim.
			// Try local source first; if that fails and internet is available,
			// retry without /Source so DISM fetches from Windows Update.
			fmt.Fprintf(&b, "echo [%d/%d] %s\r\n", i+1, len(cfg.Ops), desc)
			fmt.Fprintf(&b, "dism /Image:C:\\mnt\\boot /Add-Capability /CapabilityName:%s /Source:C:\\mnt\\install /LimitAccess 2>&1\r\n", op.Capability)
			b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
			fmt.Fprintf(&b, "    echo %s failed offline — exit code !ERRORLEVEL!\r\n", desc)
			b.WriteString("    if \"!HAS_INET!\"==\"1\" (\r\n")
			fmt.Fprintf(&b, "        echo Retrying %s via Windows Update...\r\n", desc)
			fmt.Fprintf(&b, "        dism /Image:C:\\mnt\\boot /Add-Capability /CapabilityName:%s 2>&1\r\n", op.Capability)
			b.WriteString("        if !ERRORLEVEL! neq 0 (\r\n")
			fmt.Fprintf(&b, "            echo WARNING: %s failed via Windows Update — exit code !ERRORLEVEL!\r\n", desc)
			b.WriteString("            set /a OPS_FAIL+=1\r\n")
			b.WriteString("        ) else (\r\n")
			fmt.Fprintf(&b, "            echo OK: %s (via Windows Update)\r\n", desc)
			b.WriteString("            set /a OPS_OK+=1\r\n")
			b.WriteString("        )\r\n")
			b.WriteString("    ) else (\r\n")
			fmt.Fprintf(&b, "        echo WARNING: %s failed and no internet — skipping\r\n", desc)
			b.WriteString("        set /a OPS_FAIL+=1\r\n")
			b.WriteString("    )\r\n")
			b.WriteString(") else (\r\n")
			fmt.Fprintf(&b, "    echo OK: %s (offline)\r\n", desc)
			b.WriteString("    set /a OPS_OK+=1\r\n")
			b.WriteString(")\r\n")
			b.WriteString("echo.\r\n")
			continue
		default:
			continue
		}
		fmt.Fprintf(&b, "echo [%d/%d] %s\r\n", i+1, len(cfg.Ops), desc)
		fmt.Fprintf(&b, "%s 2>&1\r\n", cmd)
		b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
		fmt.Fprintf(&b, "    echo WARNING: %s failed with exit code !ERRORLEVEL!\r\n", desc)
		b.WriteString("    set /a OPS_FAIL+=1\r\n")
		b.WriteString(") else (\r\n")
		fmt.Fprintf(&b, "    echo OK: %s\r\n", desc)
		b.WriteString("    set /a OPS_OK+=1\r\n")
		b.WriteString(")\r\n")
		b.WriteString("echo.\r\n")
	}
	b.WriteString("echo Operations: !OPS_OK! succeeded, !OPS_FAIL! failed\r\n")
	b.WriteString("echo.\r\n")

	// Verify: list enabled features
	b.WriteString("echo --- Verifying serviced image ---\r\n")
	b.WriteString("dism /Image:C:\\mnt\\boot /Get-Features 2>&1 | findstr /i \"Enabled\" 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// Unmount install.wim (discard, read-only)
	b.WriteString("echo --- Unmounting install.wim ---\r\n")
	b.WriteString("dism /Unmount-Image /MountDir:C:\\mnt\\install /Discard 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// Unmount boot.wim (commit changes)
	b.WriteString("echo --- Committing boot.wim changes ---\r\n")
	b.WriteString("dism /Unmount-Image /MountDir:C:\\mnt\\boot /Commit 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo ERROR: Failed to commit boot.wim — exit code !ERRORLEVEL!\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo boot.wim committed successfully\r\n")
	b.WriteString("echo.\r\n")

	// Copy to devcell.wim
	b.WriteString("echo --- Creating devcell.wim ---\r\n")
	b.WriteString("copy %SHARED%\\boot.wim %SHARED%\\devcell.wim 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo ERROR: Failed to create devcell.wim\r\n")
	b.WriteString("    echo FAIL > %SHARED%\\" + WimBuilderDoneFile + "\r\n")
	b.WriteString("    goto done\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo devcell.wim created\r\n")
	b.WriteString("echo.\r\n")

	// Success marker
	b.WriteString("echo === DEVCELL WIM BUILDER COMPLETE ===\r\n")
	b.WriteString("echo Operations: !OPS_OK! succeeded, !OPS_FAIL! failed\r\n")
	b.WriteString("echo SUCCESS > %SHARED%\\" + WimBuilderDoneFile + "\r\n")

	b.WriteString("\r\n:done\r\n")
	b.WriteString("echo %DATE% %TIME%\r\n")

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

// OpenSSHPrepOps returns the servicing operations to add OpenSSH to a
// boot.wim. Uses the capability name rather than a feature.
func OpenSSHPrepOps() []WimPrepOp {
	return []WimPrepOp{
		{Capability: "OpenSSH.Server~~~~0.0.1.0"},
		{Capability: "OpenSSH.Client~~~~0.0.1.0"},
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
}

// BuildWimBuilderArgv constructs the QEMU argv for the WIM builder VM.
// It attaches the WinPE ISO as the boot CD, the shared volume as USB, and
// the Windows ISO as a second USB CD for install.wim access.
func BuildWimBuilderArgv(wbs WimBuilderSpec) []string {
	argv := BuildWinPECommand(wbs.Spec, wbs.WinPEISO, wbs.SharedImg)

	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.WindowsISO+",media=cdrom,if=none,id=cdrom1",
			"-device", "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
	}
	return argv
}

// SharedVolumeFiles returns the files to place on the builder's shared FAT
// volume. The caller must also add "/boot.wim" with the actual boot.wim
// content — it's excluded here because it's large and already on disk.
func SharedVolumeFiles(cfg WimPrepConfig) map[string][]byte {
	return map[string][]byte{
		"/" + AgentVolumeMarker:    []byte("1"),
		"/" + AgentCommandFile:     []byte(WimBuilderScriptCommand()),
		"/" + WimBuilderScriptName: GenerateWimBuilderScript(cfg),
	}
}
