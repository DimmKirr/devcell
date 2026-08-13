package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ExtractWinPEStage extracts the EFI boot files and boot.wim from a Windows ISO
// into stageDir, creating the directory structure needed to build a bootable
// WinPE ISO.
func ExtractWinPEStage(winISO, stageDir string) error {
	for _, dir := range []string{
		filepath.Join(stageDir, "sources"),
		filepath.Join(stageDir, "boot"),
		filepath.Join(stageDir, "efi", "boot"),
		filepath.Join(stageDir, "efi", "microsoft", "boot"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	extractions := []struct {
		isoPath string
		dest    string
		alts    []string
	}{
		{
			isoPath: "sources/boot.wim",
			dest:    filepath.Join(stageDir, "sources", "boot.wim"),
		},
		{
			isoPath: "efi/microsoft/boot/bcd",
			dest:    filepath.Join(stageDir, "boot", "bcd"),
			alts:    []string{"EFI/Microsoft/Boot/BCD"},
		},
		{
			isoPath: "boot/boot.sdi",
			dest:    filepath.Join(stageDir, "boot", "boot.sdi"),
			alts:    []string{"Boot/boot.sdi", "BOOT/BOOT.SDI"},
		},
		{
			isoPath: "bootmgr.efi",
			dest:    filepath.Join(stageDir, "bootmgr.efi"),
			alts:    []string{"BOOTMGR.EFI"},
		},
		{
			isoPath: "efi/boot/bootaa64.efi",
			dest:    filepath.Join(stageDir, "efi", "boot", "bootaa64.efi"),
			alts:    []string{"EFI/BOOT/BOOTAA64.EFI", "EFI/Boot/bootaa64.efi"},
		},
		{
			isoPath: "efi/microsoft/boot/bcd",
			dest:    filepath.Join(stageDir, "efi", "microsoft", "boot", "bcd"),
			alts:    []string{"EFI/Microsoft/Boot/BCD"},
		},
		{
			isoPath: "efi/microsoft/boot/efisys_noprompt.bin",
			dest:    filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys_noprompt.bin"),
			alts: []string{
				"EFI/Microsoft/Boot/efisys_noprompt.bin",
				"efi/microsoft/boot/efisys.bin",
				"EFI/Microsoft/Boot/efisys.bin",
			},
		},
	}

	for _, e := range extractions {
		paths := append([]string{e.isoPath}, e.alts...)
		var data []byte
		var extractErr error
		for _, p := range paths {
			data, extractErr = Extract7z(winISO, p)
			if extractErr == nil && len(data) > 0 {
				break
			}
		}
		if extractErr != nil {
			return fmt.Errorf("extracting %s from Windows ISO: %w", e.isoPath, extractErr)
		}
		if len(data) == 0 {
			return fmt.Errorf("%s is empty in Windows ISO", e.isoPath)
		}
		if err := os.WriteFile(e.dest, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", e.dest, err)
		}
	}

	return nil
}

// Extract7z extracts a single file from an ISO using 7z.
func Extract7z(isoPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "7z-extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("7z", "e", "-o"+tmpDir, "-y", isoPath, filePath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	base := filepath.Base(filePath)
	return os.ReadFile(filepath.Join(tmpDir, base))
}
