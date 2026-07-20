package tart

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// PreflightCheck validates the host can run macOS VMs.
// Returns nil if all checks pass, or an error describing what's wrong.
// Pure function (takes OS/arch as params for testability).
func PreflightCheck(goos, goarch string) error {
	if goos != "darwin" {
		return fmt.Errorf("macOS VMs require macOS host (got %s)", goos)
	}
	if goarch != "arm64" {
		return fmt.Errorf("macOS VMs require Apple Silicon (got %s)", goarch)
	}
	return nil
}

// PreflightCheckHost calls PreflightCheck with runtime values.
func PreflightCheckHost() error {
	return PreflightCheck(runtime.GOOS, runtime.GOARCH)
}

// IPSWCacheDir returns the shared IPSW cache directory (not per-cell).
func IPSWCacheDir(home string) string {
	return filepath.Join(home, ".devcell", "cache", "ipsw")
}

// ScreenshotDir returns the directory for debug VNC screenshots.
func ScreenshotDir(projectDir string) string {
	return filepath.Join(projectDir, ".devcell", "debug", "screenshots")
}

// IPSWCachePath returns the path to the cached restore.ipsw file.
func IPSWCachePath(home string) string {
	return filepath.Join(IPSWCacheDir(home), "restore.ipsw")
}

// CellHome returns the per-cell persistent home directory.
// Layout: ~/.devcell/<cellName>/
// Same path on both Linux (bind-mount → /home/<user>) and macOS (VirtioFS → /Users/<user>).
func CellHome(home, cellName string) string {
	return filepath.Join(home, ".devcell", cellName)
}

// TemplatePaths holds paths to per-template VM platform artifacts.
// Layout: ~/.devcell/darwin/<stackTag>/
type TemplatePaths struct {
	Dir        string // parent directory
	Disk       string // disk.img
	AuxStorage string // aux-storage.img
	HWModel    string // hardware-model.json
	MachineID  string // machine-id.json
}

// NewTemplatePaths returns paths for a template's VM artifacts.
func NewTemplatePaths(home, stack string, modules []string) TemplatePaths {
	dir := TemplateDir(home, stack, modules)
	return TemplatePaths{
		Dir:        dir,
		Disk:       filepath.Join(dir, "disk.img"),
		AuxStorage: filepath.Join(dir, "aux-storage.img"),
		HWModel:    filepath.Join(dir, "hardware-model.json"),
		MachineID:  filepath.Join(dir, "machine-id.json"),
	}
}

// Exists checks if all required template artifact files exist.
func (tp TemplatePaths) Exists() bool {
	for _, p := range []string{tp.Disk, tp.AuxStorage, tp.HWModel, tp.MachineID} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// MissingFiles returns a list of template artifact files that don't exist on disk.
func (tp TemplatePaths) MissingFiles() []string {
	var missing []string
	for _, p := range []string{tp.Disk, tp.AuxStorage, tp.HWModel, tp.MachineID} {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, filepath.Base(p))
		}
	}
	return missing
}

// CellSSHPaths holds paths to per-cell SSH keys.
// Layout: ~/.devcell/<cellName>/.ssh/
type CellSSHPaths struct {
	Dir        string // ~/.devcell/<cellName>/.ssh/
	PrivateKey string // id_ed25519
	PublicKey  string // id_ed25519.pub
}

// NewCellSSHPaths returns paths for a cell's SSH keys.
func NewCellSSHPaths(home, cellName string) CellSSHPaths {
	dir := filepath.Join(CellHome(home, cellName), ".ssh")
	key := filepath.Join(dir, "id_ed25519")
	return CellSSHPaths{
		Dir:        dir,
		PrivateKey: key,
		PublicKey:  key + ".pub",
	}
}

// ArtifactPaths holds the paths to all VM artifact files.
// Deprecated: use TemplatePaths + CellSSHPaths instead.
type ArtifactPaths struct {
	Dir           string // parent directory
	Disk          string // disk.img
	AuxStorage    string // aux-storage.img
	HWModel       string // hardware-model.json
	MachineID     string // machine-id.json
	SSHPrivateKey string // id_ed25519 (per-cell generated key)
	SSHPublicKey  string // id_ed25519.pub
}

// NewArtifactPaths returns paths for a cell's Darwin VM artifacts.
// Deprecated: use NewTemplatePaths + NewCellSSHPaths instead.
func NewArtifactPaths(home, cellName string) ArtifactPaths {
	dir := ArtifactDir(home, cellName)
	sshKey := filepath.Join(dir, "id_ed25519")
	return ArtifactPaths{
		Dir:           dir,
		Disk:          filepath.Join(dir, "disk.img"),
		AuxStorage:    filepath.Join(dir, "aux-storage.img"),
		HWModel:       filepath.Join(dir, "hardware-model.json"),
		MachineID:     filepath.Join(dir, "machine-id.json"),
		SSHPrivateKey: sshKey,
		SSHPublicKey:  sshKey + ".pub",
	}
}

// Exists checks if all required artifact files exist.
func (a ArtifactPaths) Exists() bool {
	for _, p := range []string{a.Disk, a.AuxStorage, a.HWModel, a.MachineID} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// MissingFiles returns a list of artifact files that don't exist on disk.
func (a ArtifactPaths) MissingFiles() []string {
	var missing []string
	for _, p := range []string{a.Disk, a.AuxStorage, a.HWModel, a.MachineID} {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, filepath.Base(p))
		}
	}
	return missing
}

// LoadArtifacts loads artifact paths and validates they exist.
// Returns an error with a helpful message if any are missing.
func LoadArtifacts(home, cellName string) (ArtifactPaths, error) {
	paths := NewArtifactPaths(home, cellName)
	missing := paths.MissingFiles()
	if len(missing) > 0 {
		return paths, fmt.Errorf("darwin VM not initialized for cell %q: missing %v — run 'cell init --engine=tart' first", cellName, missing)
	}
	return paths, nil
}
