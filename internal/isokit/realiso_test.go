package isokit

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func windowsISOPath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.ExpandEnv("$HOME/.devcell/cache/qemu/windows-arm64-en-us.iso"),
		"/home/dmitry/.devcell/cache/qemu/windows-arm64-en-us.iso",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("Windows ISO not available")
	return ""
}

func TestDiagnoseISO_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)
	diag := DiagnoseISO(isoPath)
	t.Logf("ISO diagnosis:\n%s", diag)

	assert.Contains(t, diag, "CD001", "should find ISO 9660 descriptors")
	assert.Contains(t, diag, "BOOTAA64.EFI string found", "should find BOOTAA64.EFI in raw scan")
}

func TestReadFileFromISO_RealWindowsISO_AllLayers(t *testing.T) {
	isoPath := windowsISOPath(t)

	t.Run("diskfs", func(t *testing.T) {
		data, err := readFileFromISODiskfs(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
		if err != nil {
			t.Logf("diskfs FAILED: %v", err)
		} else {
			t.Logf("diskfs OK: %d bytes", len(data))
		}
	})

	t.Run("raw", func(t *testing.T) {
		data, err := readFileFromISORaw(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
		if err != nil {
			t.Errorf("raw reader FAILED: %v", err)
		} else {
			t.Logf("raw OK: %d bytes, magic=%c%c", len(data), data[0], data[1])
		}
	})

	t.Run("combined", func(t *testing.T) {
		data, err := ReadFileFromISO(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
		require.NoError(t, err, "ReadFileFromISO should extract BOOTAA64.EFI")
		assert.True(t, len(data) > 1000, "BOOTAA64.EFI should be substantial (got %d bytes)", len(data))
		assert.Equal(t, byte('M'), data[0])
		assert.Equal(t, byte('Z'), data[1])
		t.Logf("combined OK: %d bytes", len(data))
	})
}
