package qemu

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverRunningVMs_Empty(t *testing.T) {
	home := t.TempDir()
	vms := DiscoverRunningVMs(home)
	assert.Empty(t, vms)
}

func TestDiscoverRunningVMs_NoPortMeta(t *testing.T) {
	home := t.TempDir()
	// Create cell dir with windows subdir but no ports.json
	cellDir := filepath.Join(home, ".devcell", "main", "windows")
	require.NoError(t, os.MkdirAll(cellDir, 0755))
	vms := DiscoverRunningVMs(home)
	assert.Empty(t, vms)
}

func TestDiscoverRunningVMs_WithPortMeta(t *testing.T) {
	home := t.TempDir()
	cellDir := filepath.Join(home, ".devcell", "main", "windows")
	require.NoError(t, os.MkdirAll(cellDir, 0755))

	pm := PortMeta{SSHPort: 10122, VNCPort: 10150, RDPPort: 10189}
	require.NoError(t, WritePortMeta(cellDir, pm))

	// Write a PID file with our own PID (so it appears "running")
	require.NoError(t, os.WriteFile(
		filepath.Join(cellDir, "qemu.pid"),
		[]byte(strconv.Itoa(os.Getpid())),
		0644,
	))

	vms := DiscoverRunningVMs(home)
	require.Len(t, vms, 1)
	assert.Equal(t, "main", vms[0].CellName)
	assert.Equal(t, uint16(10150), vms[0].Ports.VNCPort)
	assert.Equal(t, uint16(10189), vms[0].Ports.RDPPort)
	assert.Equal(t, uint16(10122), vms[0].Ports.SSHPort)
}

func TestDiscoverRunningVMs_MultipleCells(t *testing.T) {
	home := t.TempDir()

	for _, cell := range []string{"main", "work"} {
		cellDir := filepath.Join(home, ".devcell", cell, "windows")
		require.NoError(t, os.MkdirAll(cellDir, 0755))
		pm := PortMeta{SSHPort: 10122, VNCPort: 10150, RDPPort: 10189}
		require.NoError(t, WritePortMeta(cellDir, pm))
		require.NoError(t, os.WriteFile(
			filepath.Join(cellDir, "qemu.pid"),
			[]byte(strconv.Itoa(os.Getpid())),
			0644,
		))
	}

	vms := DiscoverRunningVMs(home)
	assert.Len(t, vms, 2)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.CellName] = true
	}
	assert.True(t, names["main"])
	assert.True(t, names["work"])
}

func TestDiscoverRunningVMs_StalePID(t *testing.T) {
	home := t.TempDir()
	cellDir := filepath.Join(home, ".devcell", "main", "windows")
	require.NoError(t, os.MkdirAll(cellDir, 0755))

	pm := PortMeta{SSHPort: 10122, VNCPort: 10150, RDPPort: 10189}
	require.NoError(t, WritePortMeta(cellDir, pm))

	// Write a PID file with a definitely-dead PID
	require.NoError(t, os.WriteFile(
		filepath.Join(cellDir, "qemu.pid"),
		[]byte("999999999"),
		0644,
	))

	vms := DiscoverRunningVMs(home)
	assert.Empty(t, vms, "stale PID should not appear as running")
}
