package qemu

import (
	"os"
	"path/filepath"
)

// DiscoveredVM represents a running QEMU VM found during discovery.
type DiscoveredVM struct {
	CellName string
	Ports    PortMeta
}

// DiscoverRunningVMs scans ~/.devcell/<cell>/windows/ directories for running
// QEMU VMs with valid PID files and port metadata.
func DiscoverRunningVMs(home string) []DiscoveredVM {
	devcellDir := filepath.Join(home, ".devcell")
	entries, err := os.ReadDir(devcellDir)
	if err != nil {
		return nil
	}

	var vms []DiscoveredVM
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cellName := entry.Name()
		// Skip non-cell directories (cache, windows template dirs)
		if cellName == "cache" || cellName == "windows" {
			continue
		}

		windowsDir := filepath.Join(devcellDir, cellName, "windows")
		if _, err := os.Stat(windowsDir); err != nil {
			continue
		}

		pid, err := ReadPIDFile(windowsDir)
		if err != nil {
			continue
		}
		if !IsProcessAlive(pid) {
			continue
		}

		pm, err := ReadPortMeta(windowsDir)
		if err != nil {
			continue
		}

		vms = append(vms, DiscoveredVM{
			CellName: cellName,
			Ports:    pm,
		})
	}
	return vms
}
