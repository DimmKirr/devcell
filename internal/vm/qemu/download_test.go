package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseISOFilename(t *testing.T) {
	tests := []struct {
		name, version, arch string
	}{
		{"Win11_24H2_EnglishInternational_Arm64.iso", "24H2", "Arm64"},
		{"Win11_24H2_English_x64.iso", "24H2", "x64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := ParseISOFilename(tt.name)
			assert.Equal(t, tt.version, meta.Version)
			assert.Equal(t, tt.arch, meta.Arch)
		})
	}
}

func TestParseISOFilename_Invalid(t *testing.T) {
	meta := ParseISOFilename("random-file.iso")
	assert.Empty(t, meta.Version)
	assert.Empty(t, meta.Arch)
}

func TestHasDownloadMarker(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.iso")

	assert.False(t, hasDownloadMarker(path))

	require.NoError(t, os.WriteFile(path+".done", []byte("ok"), 0644))

	assert.True(t, hasDownloadMarker(path))
}

func TestValidateISO_RejectsNonISO(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "fake.iso")
	require.NoError(t, os.WriteFile(path, make([]byte, 0x9000), 0644))

	err := ValidateISO(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a recognised disc image")
}

func TestValidateISO_RejectsSmallFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "tiny.iso")
	require.NoError(t, os.WriteFile(path, []byte("tiny"), 0644))

	err := ValidateISO(path)
	assert.Error(t, err)
}

func TestValidateISO_AcceptsValidMagic(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "valid.iso")
	data := make([]byte, 0x9000)
	copy(data[0x8001:], "CD001")
	require.NoError(t, os.WriteFile(path, data, 0644))

	assert.NoError(t, ValidateISO(path))
}

func TestValidateISO_AcceptsUDF(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "udf.iso")
	data := make([]byte, 0x9000)
	copy(data[0x8001:], "BEA01")
	require.NoError(t, os.WriteFile(path, data, 0644))

	assert.NoError(t, ValidateISO(path))
}

func TestResolveWindowsISO_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")
	data := make([]byte, 0x9000)
	copy(data[0x8001:], "CD001")
	require.NoError(t, os.WriteFile(isoPath, data, 0644))

	result, err := ResolveWindowsISO(isoPath, "/some/toml/path.iso", "")
	require.NoError(t, err)
	assert.Equal(t, isoPath, result)
}

func TestResolveWindowsISO_FallsBackToConfig(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "win.iso")
	data := make([]byte, 0x9000)
	copy(data[0x8001:], "CD001")
	require.NoError(t, os.WriteFile(isoPath, data, 0644))

	result, err := ResolveWindowsISO("", isoPath, "")
	require.NoError(t, err)
	assert.Equal(t, isoPath, result)
}

func TestResolveWindowsISO_FallsBackToCache(t *testing.T) {
	tmpDir := t.TempDir()
	cached := WindowsISOPath(tmpDir, "en-us")
	require.NoError(t, os.MkdirAll(filepath.Dir(cached), 0755))
	data := make([]byte, 0x9000)
	copy(data[0x8001:], "CD001")
	require.NoError(t, os.WriteFile(cached, data, 0644))
	require.NoError(t, os.WriteFile(cached+".done", []byte("ok"), 0644))

	result, err := ResolveWindowsISO("", "", tmpDir)
	require.NoError(t, err)
	assert.Equal(t, cached, result)
}

func TestResolveWindowsISO_MissingReturnsErrorWithURL(t *testing.T) {
	_, err := ResolveWindowsISO("", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), WindowsISODownloadURL)
	assert.Contains(t, err.Error(), "cell init --engine=qemu")
}

func TestResolveWindowsISO_FileNotFound(t *testing.T) {
	_, err := ResolveWindowsISO("/nonexistent/win.iso", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestWindowsISODownloadURL_IsSet(t *testing.T) {
	assert.NotEmpty(t, WindowsISODownloadURL)
	assert.Contains(t, WindowsISODownloadURL, "microsoft.com")
}

func TestVirtioDriversURL_IsSet(t *testing.T) {
	assert.NotEmpty(t, VirtioDriversURL)
	assert.Contains(t, VirtioDriversURL, "virtio-win.iso")
}

func TestCacheDir(t *testing.T) {
	dir := CacheDir("/home/user")
	assert.Contains(t, dir, ".devcell/cache/qemu")
}

func TestRemoveDownloadMarkers(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, ".devcell", "cache", "qemu")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "virtio-win.iso.done"), []byte("ok"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "virtio-win.iso"), []byte("data"), 0644))

	RemoveDownloadMarkers(tmpDir)

	_, err := os.Stat(filepath.Join(cacheDir, "virtio-win.iso.done"))
	assert.True(t, os.IsNotExist(err), ".done marker should be removed")
	_, err = os.Stat(filepath.Join(cacheDir, "virtio-win.iso"))
	assert.NoError(t, err, "ISO file should remain")
}

func TestWindowsISOPath(t *testing.T) {
	path := WindowsISOPath("/home/user", "en-us")
	assert.Equal(t, "/home/user/.devcell/cache/qemu/windows-arm64-en-us.iso", path)

	path = WindowsISOPath("/home/user", "de-de")
	assert.Equal(t, "/home/user/.devcell/cache/qemu/windows-arm64-de-de.iso", path)
}

// writeBootableCachedISO plants a cache entry that passes the reuse check:
// since run 20260812T081924 a cache hit requires firmware-bootable media
// (El Torito EFI catalog), not just a .done marker.
func writeBootableCachedISO(t *testing.T, cached string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(cached), 0755))
	img := make([]byte, 64*2048)
	for sector, magic := range map[int]string{16: "BEA01", 17: "NSR02", 18: "TEA01"} {
		copy(img[sector*2048+1:], magic)
		img[sector*2048+6] = 0x01
	}
	require.NoError(t, os.WriteFile(cached, img, 0644))
	require.NoError(t, isokit.AddElToritoEFIBoot(cached, []byte("boot-image")))
	require.NoError(t, os.WriteFile(cached+".done", []byte("ok"), 0644))
}

func TestDownloadWindowsISO_CacheHit(t *testing.T) {
	tmpDir := t.TempDir()
	cached := WindowsISOPath(tmpDir, "en-us")
	writeBootableCachedISO(t, cached)

	path, err := DownloadWindowsISO(nil, tmpDir, "en-us", false, NopObserver{})
	require.NoError(t, err)
	assert.Equal(t, cached, path)
}

func TestDownloadWindowsISO_DefaultLanguage(t *testing.T) {
	tmpDir := t.TempDir()
	cached := WindowsISOPath(tmpDir, "en-us")
	writeBootableCachedISO(t, cached)

	path, err := DownloadWindowsISO(nil, tmpDir, "", false, NopObserver{})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, "en-us.iso"))
}

func TestPwshConstants(t *testing.T) {
	assert.Contains(t, PwshReleaseURL, "PowerShell")
	assert.Contains(t, PwshReleaseURL, "arm64")
	assert.Contains(t, PwshReleaseURL, PwshVersion)
}

func TestPwshZipPath(t *testing.T) {
	path := PwshZipPath("/home/user")
	assert.Contains(t, path, ".devcell/cache/qemu")
	assert.Contains(t, path, PwshZipName)
}

func TestAlpineConstants(t *testing.T) {
	assert.Contains(t, AlpineRootfsURL, "alpine")
	assert.Contains(t, AlpineRootfsURL, "aarch64")
	assert.Contains(t, AlpineRootfsURL, AlpineVersion)
}

func TestAlpineRootfsPath(t *testing.T) {
	path := AlpineRootfsPath("/home/user")
	assert.Contains(t, path, ".devcell/cache/qemu")
	assert.Contains(t, path, AlpineRootfsName)
}
