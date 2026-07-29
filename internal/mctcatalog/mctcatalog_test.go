package mctcatalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFetchWindowsISO_Download is a long test: downloads a ~4.3 GB ESD from
// Microsoft CDN, assembles a bootable ISO, and validates the result.
// Run with: go test -tags wimlib -run TestFetchWindowsISO_Download ./internal/mctcatalog/
func TestFetchWindowsISO_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("long: downloads ~4.3 GB Windows ESD from Microsoft CDN")
	}
	requireWimlib(t)
	requireISOTool(t)
	requireCabextract(t)

	cacheDir := t.TempDir()

	isoPath, err := FetchWindowsISO(context.Background(), FetchConfig{
		CacheDir: cacheDir,
		Language: "en-us",
		Edition:  "Professional",
		LogFunc:  func(f string, a ...any) { t.Logf(f, a...) },
		OnProgress: func(filename string, downloaded, total int64) {
			if total > 0 {
				t.Logf("  %s: %.0f / %.0f MB (%.1f%%)",
					filename,
					float64(downloaded)/(1024*1024),
					float64(total)/(1024*1024),
					float64(downloaded)/float64(total)*100)
			}
		},
	})
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)

	// A valid Windows 11 ARM64 ISO should be at least 4 GB.
	const minISOSize = 4 * 1024 * 1024 * 1024 // 4 GB
	assert.Greater(t, info.Size(), int64(minISOSize),
		"Windows ISO should be at least 4 GB (got %.1f GB)", float64(info.Size())/(1024*1024*1024))

	assertValidWindowsISO(t, isoPath)
}

// TestAssembleMCTISO_FromCachedESD is a short test: uses a pre-downloaded ESD
// (set DEVCELL_TEST_ESD_PATH) to test the assembly pipeline without network.
// Run with: DEVCELL_TEST_ESD_PATH=/path/to/windows.esd go test -tags wimlib -run TestAssembleMCTISO_FromCachedESD ./internal/mctcatalog/
func TestAssembleMCTISO_FromCachedESD(t *testing.T) {
	esdPath := os.Getenv("DEVCELL_TEST_ESD_PATH")
	if esdPath == "" {
		t.Skip("set DEVCELL_TEST_ESD_PATH to a Windows ARM64 MCT ESD file")
	}
	requireWimlib(t)
	requireISOTool(t)

	info, err := os.Stat(esdPath)
	require.NoError(t, err, "ESD file not found: %s", esdPath)
	t.Logf("ESD: %s (%.1f GB)", esdPath, float64(info.Size())/(1024*1024*1024))

	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "test-windows.iso")

	err = AssembleMCTISO(context.Background(), esdPath, AssembleConfig{
		WorkDir: tmpDir,
		ISOPath: isoPath,
		Label:   "W11_TEST",
		LogFunc: func(f string, a ...any) { t.Logf(f, a...) },
	})
	require.NoError(t, err)

	assertValidWindowsISO(t, isoPath)
}

// assertValidWindowsISO checks that an ISO file has the properties needed
// to boot Windows Setup in UEFI mode.
func assertValidWindowsISO(t *testing.T, isoPath string) {
	t.Helper()

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "ISO must not be empty")

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()

	// 1. ISO 9660 primary volume descriptor (CD001 at sector 16, offset 0x8001)
	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic), "ISO must have ISO 9660 PVD (CD001)")

	// 2. UDF markers — scan for BEA01, NSR02, or NSR03
	hasUDF := false
	buf := make([]byte, 2048)
	for sector := 16; sector < 256; sector++ {
		if _, err := f.ReadAt(buf, int64(sector)*2048); err != nil {
			break
		}
		tag := string(buf[1:6])
		if tag == "BEA01" || tag == "NSR02" || tag == "NSR03" {
			hasUDF = true
			break
		}
	}
	assert.True(t, hasUDF, "ISO must have UDF filesystem — install.wim can exceed ISO 9660's 4GB limit")

	// 3. File size sanity — the ISO should be bigger than the minimum
	// meaningful Windows installer (~100 MB for stage dir alone)
	assert.Greater(t, info.Size(), int64(100*1024*1024),
		"ISO seems too small for a Windows installer (%.1f MB)", float64(info.Size())/(1024*1024))
}

func requireWimlib(t *testing.T) {
	t.Helper()
	if !wimlib.Available() {
		t.Skip("wimlib not available: build with -tags wimlib and install wimlib")
	}
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

func requireCabextract(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cabextract"); err != nil {
		t.Skip("cabextract not on PATH")
	}
}
