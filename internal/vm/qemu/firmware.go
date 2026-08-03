package qemu

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// KernelFirmwareCacheName is where a kernel-bootable EDK2 image lives in the
// devcell cache (~/.devcell/cache/qemu/). Named distinctly from QEMU_EFI.fd
// on purpose: every distro ships a *different, incompatible* build under that
// name, and telling them apart by filename is exactly the trap.
const KernelFirmwareCacheName = "QEMU_EFI.kernel.fd"

// CheckKernelBootableFirmware verifies that path holds the ArmVirtQemuKernel
// EDK2 build — the relocatable image with the ARM64 kernel-image magic
// ("ARMd" at offset 56) that QEMU's -kernel loader understands. The common
// ArmVirtQemu build (what nixpkgs, Debian and openSUSE all ship as
// QEMU_EFI.fd) is linked for flash address 0 and boots to *silence* when
// loaded into DRAM, so this must be checked, not assumed.
func CheckKernelBootableFirmware(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("kernel firmware: %w", err)
	}
	defer f.Close()
	header := make([]byte, 60)
	if _, err := f.ReadAt(header, 0); err != nil {
		return fmt.Errorf("kernel firmware %s: reading header: %w", path, err)
	}
	if !bytes.Equal(header[56:60], []byte("ARMd")) {
		return fmt.Errorf(
			"%s is not an ArmVirtQemuKernel build (no ARM64 kernel-image magic at offset 56) — "+
				"it would boot to silence on the secure=on machine; build one with: "+
				`nix build --impure --expr 'let pkgs = (builtins.getFlake "nixpkgs").legacyPackages.$`+
				`{builtins.currentSystem}; in (pkgs.OVMF.override { projectDscPath = "ArmVirtPkg/ArmVirtQemuKernel.dsc"; }).fd'`,
			path)
	}
	return nil
}

// KernelFirmwarePath resolves the firmware for the WSL2 machine (secure=on +
// -kernel): $DEVCELL_QEMU_EFI_KERNEL if set, else the devcell cache. An
// explicit override that fails validation is an error rather than a
// fallthrough — the user pointed at a specific file, and using another would
// hide the mistake behind a silent boot failure later.
func KernelFirmwarePath() (string, error) {
	if p := os.Getenv("DEVCELL_QEMU_EFI_KERNEL"); p != "" {
		if err := CheckKernelBootableFirmware(p); err != nil {
			return "", err
		}
		return p, nil
	}
	home, _ := os.UserHomeDir()
	cached := filepath.Join(home, ".devcell", "cache", "qemu", KernelFirmwareCacheName)
	if err := CheckKernelBootableFirmware(cached); err != nil {
		return "", fmt.Errorf(
			"no kernel-bootable firmware: set DEVCELL_QEMU_EFI_KERNEL or place one at %s (%w)",
			cached, err)
	}
	return cached, nil
}
