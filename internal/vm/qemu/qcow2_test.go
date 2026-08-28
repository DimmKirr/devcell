package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoQemuImg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not found")
	}
}

func TestCreateFATQcow2_CreatesQcow2Format(t *testing.T) {
	skipIfNoQemuImg(t)

	imgPath := filepath.Join(t.TempDir(), "test.qcow2")
	files := map[string][]byte{
		"/hello.txt": []byte("world"),
	}
	require.NoError(t, CreateFATQcow2(imgPath, files, 256*1024*1024))

	out, err := exec.Command("qemu-img", "info", "--output=json", imgPath).Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), `"format": "qcow2"`)
}

func TestCreateFATQcow2_VirtualSizeMatchesCapacity(t *testing.T) {
	skipIfNoQemuImg(t)

	imgPath := filepath.Join(t.TempDir(), "test.qcow2")
	files := map[string][]byte{
		"/marker.txt": []byte("1"),
	}
	capacity := int64(512 * 1024 * 1024)
	require.NoError(t, CreateFATQcow2(imgPath, files, capacity))

	out, err := exec.Command("qemu-img", "info", imgPath).Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "virtual size: 512 MiB")
}

func TestCreateFATQcow2_SparseOnDisk(t *testing.T) {
	skipIfNoQemuImg(t)

	imgPath := filepath.Join(t.TempDir(), "test.qcow2")
	files := map[string][]byte{
		"/small.txt": []byte("tiny file"),
	}
	capacity := int64(1024 * 1024 * 1024) // 1GB virtual
	require.NoError(t, CreateFATQcow2(imgPath, files, capacity))

	info, err := os.Stat(imgPath)
	require.NoError(t, err)
	assert.Less(t, info.Size(), int64(50*1024*1024),
		"qcow2 on-disk size should be much less than 1GB virtual")
}

func TestCreateFATQcow2_RoundtripFiles(t *testing.T) {
	skipIfNoQemuImg(t)

	imgPath := filepath.Join(t.TempDir(), "test.qcow2")
	wantContent := "hello from qcow2 roundtrip test"
	files := map[string][]byte{
		"/test.txt":   []byte(wantContent),
		"/marker.txt": []byte("1"),
	}
	require.NoError(t, CreateFATQcow2(imgPath, files, 128*1024*1024))

	got, err := ReadFileFromFATQcow2(imgPath, "/test.txt")
	require.NoError(t, err)
	assert.Equal(t, wantContent, string(got))

	marker, err := ReadFileFromFATQcow2(imgPath, "/marker.txt")
	require.NoError(t, err)
	assert.Equal(t, "1", string(marker))
}

func TestReadFileFromFATQcow2_MissingFile(t *testing.T) {
	skipIfNoQemuImg(t)

	imgPath := filepath.Join(t.TempDir(), "test.qcow2")
	files := map[string][]byte{
		"/exists.txt": []byte("here"),
	}
	require.NoError(t, CreateFATQcow2(imgPath, files, 128*1024*1024))

	_, err := ReadFileFromFATQcow2(imgPath, "/no-such-file.txt")
	assert.Error(t, err)
}

func TestCreateFATQcow2_CleansUpRawIntermediate(t *testing.T) {
	skipIfNoQemuImg(t)

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.qcow2")
	files := map[string][]byte{
		"/f.txt": []byte("data"),
	}
	require.NoError(t, CreateFATQcow2(imgPath, files, 128*1024*1024))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".raw"),
			"intermediate .raw file should be cleaned up: %s", e.Name())
	}
}
