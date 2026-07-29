package qemu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	pid := 54321

	err := WritePIDFile(dir, pid)
	require.NoError(t, err)

	got, err := ReadPIDFile(dir)
	require.NoError(t, err)
	assert.Equal(t, pid, got)
}

func TestReadPIDFile_Missing(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadPIDFile(dir)
	assert.Error(t, err)
}

func TestCleanStalePIDFile_DeadProcess(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WritePIDFile(dir, 99999999))

	err := CleanStalePIDFile(dir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "qemu.pid"))
	assert.True(t, os.IsNotExist(err), "PID file should be removed for dead process")
}

func TestCleanStalePIDFile_AliveProcess(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WritePIDFile(dir, os.Getpid()))

	err := CleanStalePIDFile(dir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "qemu.pid"))
	assert.NoError(t, err, "PID file should be kept for alive process")
}

func TestCleanStalePIDFile_NoPIDFile(t *testing.T) {
	dir := t.TempDir()

	err := CleanStalePIDFile(dir)
	assert.NoError(t, err, "should be no-op when PID file doesn't exist")
}

func TestIsProcessAlive_Self(t *testing.T) {
	assert.True(t, IsProcessAlive(os.Getpid()))
}

func TestIsProcessAlive_Dead(t *testing.T) {
	assert.False(t, IsProcessAlive(99999999))
}

func TestWritePIDFile(t *testing.T) {
	dir := t.TempDir()
	pid := 12345

	err := WritePIDFile(dir, pid)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "qemu.pid"))
	require.NoError(t, err)

	got := strings.TrimSpace(string(data))
	assert.Equal(t, strconv.Itoa(pid), got)
}
