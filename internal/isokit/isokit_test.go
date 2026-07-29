package isokit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSimpleISO_ContainsFile(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "test.iso")

	content := []byte("<xml>autounattend</xml>")
	err := CreateSimpleISO(isoPath, map[string][]byte{
		"/autounattend.xml": content,
	})
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	data, err := ReadFileFromISO(isoPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestCreateSimpleISO_MultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "multi.iso")

	files := map[string][]byte{
		"/file1.txt":        []byte("hello"),
		"/subdir/file2.txt": []byte("world"),
	}
	err := CreateSimpleISO(isoPath, files)
	require.NoError(t, err)

	for path, expected := range files {
		data, err := ReadFileFromISO(isoPath, path)
		require.NoError(t, err, "reading %s", path)
		assert.Equal(t, expected, data, "content mismatch for %s", path)
	}
}

func TestCreateSimpleISO_EmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "empty.iso")

	err := CreateSimpleISO(isoPath, map[string][]byte{})
	assert.Error(t, err, "should reject empty file map")
}

func TestCreateWindowsISO_ContainsBootFiles(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "sources"), 0o755))

	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "boot.wim"), []byte("boot-wim-data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "install.wim"), []byte("install-wim-data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "setup.exe"), []byte("setup"), 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "MYWINISO")
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "ISO must not be empty")
}

func TestCreateWindowsISO_HasUDF(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "sources"), 0o755))

	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "sources", "boot.wim"), []byte("boot-wim-data"), 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "TESTLABEL")
	require.NoError(t, err)

	assert.True(t, isoHasUDF(t, isoPath),
		"ISO must contain UDF — install.wim can exceed ISO 9660's 4GB file size limit")
}

// isoHasUDF scans the volume descriptor area for BEA01 (UDF Volume Recognition
// Sequence) or NSR02/NSR03 (UDF structure descriptors). Both genisoimage and
// hdiutil place these in the first 64 sectors.
func isoHasUDF(t *testing.T, isoPath string) bool {
	t.Helper()
	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 2048)
	for sector := 16; sector < 256; sector++ {
		_, err := f.ReadAt(buf, int64(sector)*2048)
		if err != nil {
			break
		}
		tag := string(buf[1:6])
		if tag == "BEA01" || tag == "NSR02" || tag == "NSR03" {
			return true
		}
	}
	return false
}

func requireISOTool(t *testing.T) {
	t.Helper()
	for _, name := range []string{"hdiutil", "genisoimage", "mkisofs"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no ISO tool available (need hdiutil, genisoimage, or mkisofs)")
}

func TestCreateWindowsISO_HasISO9660Magic(t *testing.T) {
	requireISOTool(t)

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")

	stageDir := filepath.Join(tmpDir, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stageDir, "efi", "microsoft", "boot"), 0o755))
	efiBin := make([]byte, 4096)
	copy(efiBin, []byte("EFI-BOOT"))
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "efi", "microsoft", "boot", "efisys.bin"), efiBin, 0o644))

	err := CreateWindowsISO(isoPath, stageDir, "TESTLABEL")
	require.NoError(t, err)

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}

func TestCreateFATImage_ContainsFile(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.img")

	content := []byte("startup.nsh content")
	err := CreateFATImage(imgPath, map[string][]byte{
		"/startup.nsh": content,
	})
	require.NoError(t, err)

	info, err := os.Stat(imgPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	data, err := ReadFileFromFAT(imgPath, "/startup.nsh")
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestCreateFATImage_MultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "multi.img")

	files := map[string][]byte{
		"/startup.nsh":      []byte("FS0:\\EFI\\BOOT\\BOOTAA64.EFI"),
		"/autounattend.xml": []byte("<xml>test</xml>"),
	}
	err := CreateFATImage(imgPath, files)
	require.NoError(t, err)

	for path, expected := range files {
		data, err := ReadFileFromFAT(imgPath, path)
		require.NoError(t, err, "reading %s", path)
		assert.Equal(t, expected, data, "content mismatch for %s", path)
	}
}

func TestCreateFATImage_EmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "empty.img")

	err := CreateFATImage(imgPath, map[string][]byte{})
	assert.Error(t, err, "should reject empty file map")
}

func TestCreateSimpleISO_HasISO9660Magic(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "magic.iso")

	err := CreateSimpleISO(isoPath, map[string][]byte{
		"/test.txt": []byte("data"),
	})
	require.NoError(t, err)

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}
