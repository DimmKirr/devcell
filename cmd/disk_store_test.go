package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func writeTempDisk(t *testing.T, size int) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-disk-*.qcow2")
	require.NoError(t, err)
	data := make([]byte, size)
	// qcow2 magic: "QFI\xfb"
	copy(data, []byte{'Q', 'F', 'I', 0xfb})
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestDiskStoreCmd_PushAndPull(t *testing.T) {
	addr := startTestRegistry(t)
	diskPath := writeTempDisk(t, 4096)
	ref := fmt.Sprintf("%s/test/disk:v1", addr)

	rootCmd.SetArgs([]string{"disk-store", "push", "--image", ref, "--disk", diskPath})
	require.NoError(t, rootCmd.Execute())

	destPath := filepath.Join(t.TempDir(), "pulled.qcow2")
	rootCmd.SetArgs([]string{"disk-store", "pull", "--image", ref, "--disk", destPath})
	require.NoError(t, rootCmd.Execute())

	orig, err := os.ReadFile(diskPath)
	require.NoError(t, err)
	pulled, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, orig, pulled, "round-trip should produce identical file")
}

func TestDiskStoreCmd_ResolveHit(t *testing.T) {
	addr := startTestRegistry(t)
	diskPath := writeTempDisk(t, 4096)
	ref := fmt.Sprintf("%s/test/disk:v1", addr)

	rootCmd.SetArgs([]string{"disk-store", "push", "--image", ref, "--disk", diskPath})
	require.NoError(t, rootCmd.Execute())

	rootCmd.SetArgs([]string{"disk-store", "resolve", "--image", ref})
	require.NoError(t, rootCmd.Execute())
}
