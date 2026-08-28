package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/isokit"
)

// CreateFATQcow2 creates a qcow2 disk image containing a FAT32 filesystem
// with the given files. The virtual disk capacity is set to capacity bytes,
// but the on-disk qcow2 file is sparse — only clusters with actual data are
// allocated. This is useful for shared volumes that need large free space
// for guest writes (e.g. WIM builder writing devcell.wim) without consuming
// host disk upfront.
func CreateFATQcow2(imgPath string, files map[string][]byte, capacity int64) error {
	rawPath := imgPath + ".raw"
	if err := isokit.CreateFATImageSized(rawPath, files, capacity); err != nil {
		return fmt.Errorf("creating raw FAT32: %w", err)
	}
	defer os.Remove(rawPath)

	cmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, imgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting raw to qcow2: %w\n%s", err, out)
	}

	return nil
}

// ReadFileFromFATQcow2 reads a single file from a qcow2-backed FAT32 disk
// image. It converts the qcow2 to a temporary raw image, reads the file
// with the standard FAT reader, and cleans up.
func ReadFileFromFATQcow2(imgPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "qcow2-read-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "disk.raw")
	cmd := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", imgPath, rawPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("converting qcow2 to raw: %w\n%s", err, out)
	}

	return isokit.ReadFileFromFAT(rawPath, filePath)
}
