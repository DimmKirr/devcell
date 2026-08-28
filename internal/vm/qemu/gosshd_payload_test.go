package qemu

import (
	"debug/pe"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guest's SSH server is built from this repo rather than downloaded.
// The old Win32-OpenSSH payload was a pinned GitHub release; this one cannot
// drift, cannot 404, and needs no cache.
func TestBuildGoSSHDPayload_ProducesAnARM64WindowsBinary(t *testing.T) {
	path, err := BuildGoSSHDPayload(t.TempDir())
	require.NoError(t, err, "cross-compiling the gosshd payload")

	f, err := pe.Open(path)
	require.NoError(t, err, "the payload must be a PE binary")
	defer f.Close()

	assert.Equal(t, uint16(pe.IMAGE_FILE_MACHINE_ARM64), f.Machine,
		"WinPE here is ARM64; an amd64 payload would not run")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(1<<20),
		"a statically linked Go server is megabytes; a tiny file means a failed link")
}

// The payload name is what the guest script starts, so the two must agree.
func TestGoSSHDPayloadName_IsAWindowsExecutable(t *testing.T) {
	assert.Equal(t, "devcell-gosshd.exe", GoSSHDPayloadName)
}
