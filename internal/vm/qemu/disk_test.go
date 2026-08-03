package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirmwarePath_NonEmpty(t *testing.T) {
	path := FirmwarePath()
	assert.NotEmpty(t, path)
	assert.True(t, filepath.IsAbs(path), "firmware path should be absolute")
}

func TestPrepareVarsFile(t *testing.T) {
	tmpDir := t.TempDir()
	firmware := filepath.Join(tmpDir, "firmware.fd")
	require.NoError(t, os.WriteFile(firmware, []byte("UEFI firmware data"), 0644))

	vars := filepath.Join(tmpDir, "subdir", "vars.fd")
	require.NoError(t, PrepareVarsFile(firmware, vars))

	data, err := os.ReadFile(vars)
	require.NoError(t, err)
	assert.Equal(t, "UEFI firmware data", string(data))
}

func TestPrepareVarsFile_MissingFirmware(t *testing.T) {
	tmpDir := t.TempDir()
	err := PrepareVarsFile("/nonexistent/firmware.fd", filepath.Join(tmpDir, "vars.fd"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading firmware")
}

func TestDefaultDiskSizeGB(t *testing.T) {
	assert.Equal(t, 64, DefaultDiskSizeGB)
}

// The nix profile in a devcell thin cell lives at /opt/devcell, not under the
// session user's $HOME — the entrypoint copies dotfiles, not the profile. When
// that candidate was missing, requireFirmware() found nothing and every QEMU
// integration test SKIPped instead of running, which reads as "green".
func TestFirmwareCandidates_IncludesDevcellNixProfile(t *testing.T) {
	got := firmwareCandidates("/home/bob")
	assert.Contains(t, got, "/opt/devcell/.local/state/nix/profiles/profile/share/qemu/edk2-aarch64-code.fd")
}

func TestFirmwareCandidates_StillPrefersSystemPackages(t *testing.T) {
	got := firmwareCandidates("/home/bob")
	assert.Equal(t, "/usr/share/AAVMF/AAVMF_CODE.fd", got[0],
		"distro packages must keep priority over the nix profile")
	assert.Contains(t, got, "/home/bob/.local/state/nix/profiles/profile/share/qemu/edk2-aarch64-code.fd")
}

func TestFirmwareCandidates_OmitsHomePathWhenHomeUnknown(t *testing.T) {
	for _, p := range firmwareCandidates("") {
		assert.False(t, strings.HasPrefix(p, "/.local"),
			"an empty $HOME must not produce a rootless /.local/... path, got %q", p)
	}
}
