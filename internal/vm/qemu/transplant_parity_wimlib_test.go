//go:build wimlib

package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListChildren_WinSxSComponents(t *testing.T) {
	installWim := installWimFixture(t)

	wim, err := wimlib.OpenWIM(installWim)
	require.NoError(t, err)
	defer wim.Close()

	names, err := wim.ListChildren(1, `\Windows\WinSxS`)
	require.NoError(t, err)

	assert.Greater(t, len(names), 10000, "WinSxS listing suspiciously small")

	found := false
	for _, n := range names {
		if strings.HasPrefix(n, "arm64_hyperv-vmfirmware_") {
			found = true
			break
		}
	}
	assert.True(t, found, "vmfirmware component dir must appear in listing")
}

func TestResolveWinSxS(t *testing.T) {
	installWim := installWimFixture(t)

	wim, err := wimlib.OpenWIM(installWim)
	require.NoError(t, err)
	defer wim.Close()

	t.Run("normal component", func(t *testing.T) {
		path, err := resolveWinSxS(wim, 1, "hyperv-vmfirmware", "vmfirmware.dll", "")
		require.NoError(t, err)
		assert.Contains(t, path, `\Windows\WinSxS\arm64_hyperv-vmfirmware_`)
		assert.True(t, strings.HasSuffix(path, `\vmfirmware.dll`))
	})

	t.Run("truncated component with SxSFile", func(t *testing.T) {
		path, err := resolveWinSxS(wim, 1, "microsoft-windows-a..perv-computenetwork", "computenetwork.dll", "HyperV-ComputeNetwork.dll")
		require.NoError(t, err)
		assert.Contains(t, path, `\Windows\WinSxS\arm64_microsoft-windows-a..perv-computenetwork_`)
		assert.Contains(t, strings.ToLower(path), "hyperv-computenetwork.dll")
	})

	t.Run("driver in inf component", func(t *testing.T) {
		path, err := resolveWinSxS(wim, 1, "dual_wvid.inf", "Vid.sys", "")
		require.NoError(t, err)
		assert.Contains(t, path, `\Windows\WinSxS\arm64_dual_wvid.inf_`)
	})

	t.Run("nonexistent component", func(t *testing.T) {
		_, err := resolveWinSxS(wim, 1, "this-does-not-exist", "foo.dll", "")
		require.Error(t, err)
	})
}

func TestExtractParityFiles_StagesEveryFile(t *testing.T) {
	installWim := installWimFixture(t)
	dest := t.TempDir()

	require.NoError(t, ExtractParityFiles(installWim, VMPParityFiles(), dest))

	for _, f := range VMPParityFiles() {
		path := filepath.Join(dest, filepath.FromSlash(f.Dest))
		info, err := os.Stat(path)
		require.NoError(t, err, "%s must be staged", f.Dest)
		assert.Greater(t, info.Size(), int64(0), "%s must not be empty", f.Dest)
	}
}

func TestExtractParityFiles_ProducesLoadableImages(t *testing.T) {
	installWim := installWimFixture(t)
	destDir := t.TempDir()

	files := append(VMPParityFiles(), VMMSExtraFiles()...)
	require.NoError(t, ExtractParityFiles(installWim, files, destDir))

	for _, f := range files {
		path := filepath.Join(destDir, filepath.FromSlash(f.Dest))
		if strings.HasSuffix(strings.ToLower(f.Dest), ".inf") ||
			strings.HasSuffix(strings.ToLower(f.Dest), ".mof") {
			continue
		}

		data, err := os.ReadFile(path)
		require.NoError(t, err, "%s must be staged", f.Dest)
		require.GreaterOrEqual(t, len(data), 2)

		assert.Equal(t, "MZ", string(data[:2]),
			"%s is not a loadable PE (header %q)",
			f.Dest, string(data[:4]))
	}
}

func TestCopyParityFilesFromDir_StagesEveryFile(t *testing.T) {
	donorDir := donorDirFixture(t)
	destDir := t.TempDir()

	files := VMPParityFiles()
	require.NoError(t, CopyParityFilesFromDir(donorDir, files, destDir))

	for _, f := range files {
		path := filepath.Join(destDir, filepath.FromSlash(f.Dest))
		info, err := os.Stat(path)
		require.NoError(t, err, "%s must be staged", f.Dest)
		assert.Greater(t, info.Size(), int64(0), "%s must not be empty", f.Dest)
	}
}

func donorDirFixture(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	p := filepath.Join(home, ".devcell", "cache", "qemu", "vmp-donor")
	if _, err := os.Stat(filepath.Join(p, "Windows", "System32", "vmwp.exe")); err != nil {
		t.Skip("no VMP donor directory available")
	}
	return p
}
