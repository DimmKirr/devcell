package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DefaultDiskSizeGB is the default Windows VM disk size.
const DefaultDiskSizeGB = 64

// CreateDisk creates a new qcow2 disk image at the given path.
func CreateDisk(path string, sizeGB int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating disk directory: %w", err)
	}
	qemuImg, err := qemuImgPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(qemuImg, "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create: %w\n%s", err, out)
	}
	return nil
}

// CloneDisk creates a qcow2 snapshot (backing file) from a template disk.
// The instance disk is thin — only stores delta writes.
func CloneDisk(templateDisk, instanceDisk string) error {
	if err := os.MkdirAll(filepath.Dir(instanceDisk), 0755); err != nil {
		return fmt.Errorf("creating instance directory: %w", err)
	}
	qemuImg, err := qemuImgPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(qemuImg, "create", "-f", "qcow2",
		"-F", "qcow2", "-b", templateDisk, instanceDisk)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img clone: %w\n%s", err, out)
	}
	return nil
}

// DiskInfo returns basic information about a qcow2 image.
func DiskInfo(path string) (string, error) {
	qemuImg, err := qemuImgPath()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(qemuImg, "info", path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("qemu-img info: %w\n%s", err, out)
	}
	return string(out), nil
}

// firmwareCandidates lists where the ARM64 EDK2 firmware may live on Linux, in
// priority order: distro packages first, then nix profiles.
//
// /opt/devcell is the devcell thin-cell nix profile — a stable path that is
// never remounted and, unlike the session user's $HOME, actually holds the
// profile (the entrypoint copies dotfiles into $HOME, not the store).
func firmwareCandidates(home string) []string {
	candidates := []string{
		"/usr/share/AAVMF/AAVMF_CODE.fd",
		"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
		"/usr/share/edk2/aarch64/QEMU_EFI.fd",
	}
	const nixRelative = ".local/state/nix/profiles/profile/share/qemu/edk2-aarch64-code.fd"
	if home != "" {
		candidates = append(candidates, filepath.Join(home, nixRelative))
	}
	return append(candidates, filepath.Join("/opt/devcell", nixRelative))
}

// firmwareFromBinary resolves the EDK2 firmware path relative to the
// qemu-system-aarch64 binary: <prefix>/share/qemu/edk2-aarch64-code.fd.
// Works for Homebrew, Nix, distro packages — any standard install layout.
func firmwareFromBinary() string {
	bin, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(real), "..", "share", "qemu", "edk2-aarch64-code.fd")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// FirmwarePath returns the path to the EDK2 UEFI firmware for ARM64.
func FirmwarePath() string {
	if p := firmwareFromBinary(); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range firmwareCandidates(home) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/share/AAVMF/AAVMF_CODE.fd"
}

// PrepareVarsFile copies the UEFI firmware to create a writable vars store.
func PrepareVarsFile(firmwarePath, varsPath string) error {
	if err := os.MkdirAll(filepath.Dir(varsPath), 0755); err != nil {
		return fmt.Errorf("creating vars directory: %w", err)
	}
	src, err := os.ReadFile(firmwarePath)
	if err != nil {
		return fmt.Errorf("reading firmware: %w", err)
	}
	if err := os.WriteFile(varsPath, src, 0644); err != nil {
		return fmt.Errorf("writing vars: %w", err)
	}
	return nil
}

func qemuImgPath() (string, error) {
	path, err := exec.LookPath("qemu-img")
	if err != nil {
		return "", fmt.Errorf("qemu-img not found — install QEMU (brew install qemu)")
	}
	return path, nil
}
