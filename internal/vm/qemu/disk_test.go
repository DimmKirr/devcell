package qemu

import (
	"os"
	"path/filepath"
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
