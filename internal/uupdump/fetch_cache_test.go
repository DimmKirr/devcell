package uupdump

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plantCachedISO writes a pure-UDF image (optionally with an injected
// El Torito catalog) at the cache path FetchWindowsISO checks.
func plantCachedISO(t *testing.T, cacheDir string, bootable bool) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	isoPath := filepath.Join(cacheDir, "windows-arm64-en-us.iso")
	img := make([]byte, 64*2048)
	for sector, magic := range map[int]string{16: "BEA01", 17: "NSR02", 18: "TEA01"} {
		copy(img[sector*2048+1:], magic)
		img[sector*2048+6] = 0x01
	}
	require.NoError(t, os.WriteFile(isoPath, img, 0o644))
	if bootable {
		require.NoError(t, isokit.AddElToritoEFIBoot(isoPath, []byte("boot-image")))
	}
	return isoPath
}

// Run 20260812T090917: the UUP dump fallback found the orphan a failed
// hdiutil mastering left behind and returned it as a valid ISO. The
// already-exists shortcut must only accept firmware-bootable images.
func TestFetchWindowsISO_RejectsNonBootableOrphan(t *testing.T) {
	cacheDir := t.TempDir()
	isoPath := plantCachedISO(t, cacheDir, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no network: the rebuild attempt must fail fast

	_, err := FetchWindowsISO(ctx, FetchConfig{CacheDir: cacheDir})
	require.Error(t, err, "must not bless a non-bootable orphan")
	_, statErr := os.Stat(isoPath)
	assert.True(t, os.IsNotExist(statErr), "the orphan must be removed so the next attempt re-masters")
}

func TestFetchWindowsISO_AcceptsBootableCachedISO(t *testing.T) {
	cacheDir := t.TempDir()
	isoPath := plantCachedISO(t, cacheDir, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // proves the shortcut needs no network

	got, err := FetchWindowsISO(ctx, FetchConfig{CacheDir: cacheDir})
	require.NoError(t, err)
	assert.Equal(t, isoPath, got)
}
