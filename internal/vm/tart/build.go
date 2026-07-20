package tart

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuildConfig holds the parameters for building a macOS VM image.
type BuildConfig struct {
	CellName string
	HomeDir  string
	Stack    string
	Modules  []string
	CPUs     uint
	MemoryGB uint64
	SSHPort  uint16
	Username string
}

// ApplyDefaults fills zero-value fields.
func (c *BuildConfig) ApplyDefaults() {
	if c.CellName == "" {
		c.CellName = "main"
	}
	if c.Stack == "" {
		c.Stack = "base"
	}
	if c.CPUs == 0 {
		c.CPUs = 4
	}
	if c.MemoryGB == 0 {
		c.MemoryGB = 4
	}
	if c.SSHPort == 0 {
		c.SSHPort = 22
	}
	if c.Username == "" {
		c.Username = "admin"
	}
}

// Validate checks required fields.
func (c *BuildConfig) Validate() error {
	if c.HomeDir == "" {
		return fmt.Errorf("HomeDir is required")
	}
	return nil
}

// ArtifactDir returns the VM artifacts path.
func (c *BuildConfig) ArtifactDir() string {
	return ArtifactDir(c.HomeDir, c.CellName)
}

// BaseImagePath returns the path to the base disk image (from init).
func (c *BuildConfig) BaseImagePath() string {
	return filepath.Join(c.ArtifactDir(), "disk.img")
}

// BuildImagePath returns the path for the ephemeral build image.
func (c *BuildConfig) BuildImagePath() string {
	return filepath.Join(c.ArtifactDir(), "disk-build.img")
}

// FinalImagePath returns the path for the final built image.
func (c *BuildConfig) FinalImagePath() string {
	return filepath.Join(c.ArtifactDir(), ImageName(c.Stack, c.Modules))
}

// BuildPreflight checks whether a build can proceed.
func BuildPreflight(baseDisk string) error {
	if _, err := os.Stat(baseDisk); err != nil {
		return fmt.Errorf("base disk image not found: %s\n\nRun 'cell init --engine=tart' first", baseDisk)
	}
	return nil
}

// CopyProgress reports bytes copied vs total during a sparse copy.
type CopyProgress struct {
	BytesCopied int64
	TotalBytes  int64
}

func (p CopyProgress) Percent() int {
	if p.TotalBytes == 0 {
		return 0
	}
	return int(p.BytesCopied * 100 / p.TotalBytes)
}

func (p CopyProgress) String() string {
	return fmt.Sprintf("Copying %dMB / %dMB (%d%%)",
		p.BytesCopied/(1024*1024),
		p.TotalBytes/(1024*1024),
		p.Percent())
}

// SparseCopy copies src to dst, preserving sparseness where the OS supports it.
func SparseCopy(src, dst string) error {
	return SparseCopyWithProgress(src, dst, nil)
}

// SparseCopyWithProgress copies src to dst with progress reporting.
// The callback fn is called after each 1MB chunk is written.
func SparseCopyWithProgress(src, dst string, fn func(CopyProgress)) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer dstFile.Close()

	if err := dstFile.Truncate(srcInfo.Size()); err != nil {
		return fmt.Errorf("truncate dest: %w", err)
	}

	total := srcInfo.Size()
	var copied int64
	buf := make([]byte, 1024*1024)
	for {
		n, readErr := srcFile.Read(buf)
		if n > 0 {
			if _, err := dstFile.Write(buf[:n]); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			copied += int64(n)
			if fn != nil {
				fn(CopyProgress{BytesCopied: copied, TotalBytes: total})
			}
		}
		if readErr != nil {
			break
		}
	}

	return nil
}

// BuildProvisionCommands returns the SSH commands to provision a build VM.
func BuildProvisionCommands(stack string, modules []string) []string {
	return ProvisionSSHCommands(stack, modules)
}
