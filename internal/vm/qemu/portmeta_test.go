package qemu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWritePortMeta_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	pm := PortMeta{SSHPort: 10022, VNCPort: 10050, RDPPort: 10089}
	err := WritePortMeta(dir, pm)
	require.NoError(t, err)

	path := filepath.Join(dir, "ports.json")
	_, err = os.Stat(path)
	assert.NoError(t, err)
}

func TestReadPortMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pm := PortMeta{SSHPort: 10022, VNCPort: 10050, RDPPort: 10089}
	require.NoError(t, WritePortMeta(dir, pm))

	got, err := ReadPortMeta(dir)
	require.NoError(t, err)
	assert.Equal(t, pm, got)
}

func TestReadPortMeta_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadPortMeta(dir)
	assert.Error(t, err)
}

func TestReadPortMeta_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ports.json"), []byte("not json"), 0644))
	_, err := ReadPortMeta(dir)
	assert.Error(t, err)
}
