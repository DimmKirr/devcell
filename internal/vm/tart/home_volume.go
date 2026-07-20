package tart

import (
	"fmt"
	"os"
)

// EnsureHomeDir creates the CellHome directory if it doesn't exist.
// Returns the path to the (existing or newly created) directory.
// Replaces EnsureHomeVolume — home is now a VirtioFS directory mount, not a disk image.
func EnsureHomeDir(home, cellName string) (string, error) {
	dir := CellHome(home, cellName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating cell home directory: %w", err)
	}
	return dir, nil
}

// GenerateHomeMountScript returns a guest-side script that mounts the
// CellHome VirtioFS share at /Users/<username>.
// The host passes --dir home:<cellHome> to tart run.
func GenerateHomeMountScript(cellName, username string) string {
	return GenerateVirtioFSMountScript("home", fmt.Sprintf("/Users/%s", username))
}
