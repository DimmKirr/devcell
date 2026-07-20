package tart

import (
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
)

// Spec holds everything needed to configure and connect to a macOS VM.
type Spec struct {
	VMName        string
	CPUs          uint
	MemoryGB      uint64
	DiskPath      string            // path to disk.img
	AuxPath       string            // path to aux-storage.img
	HWModelPath   string            // path to hardware-model.json
	MachineIDPath string            // path to machine-id.json
	SharedDirs    map[string]string // tag -> host path (VirtioFS)
	SSHPort       uint16            // forwarded port for SSH (default 22)
	SSHUser       string            // guest username (default "devcell")
	MACAddr       string            // reuse a specific MAC (colon-separated); empty = random
	Binary        string            // agent binary (claude, zsh, etc.)
	DefaultFlags  []string
	UserArgs      []string
	EnvVars       []string // KEY=VALUE pairs
	ProjectDir    string   // host project directory
	SSHKeyPath    string   // path to SSH private key (optional; adds -i flag)
}

// ApplyDefaults fills in zero-value fields with sensible defaults.
func (s *Spec) ApplyDefaults() {
	if s.CPUs == 0 {
		s.CPUs = 4
	}
	if s.MemoryGB == 0 {
		s.MemoryGB = 4
	}
	if s.SSHPort == 0 {
		s.SSHPort = 22
	}
	if s.SSHUser == "" {
		s.SSHUser = "devcell"
	}
}

// Validate returns an error if required fields are missing.
func (s *Spec) Validate() error {
	if s.DiskPath == "" {
		return fmt.Errorf("DiskPath is required")
	}
	if s.CPUs == 0 {
		return fmt.Errorf("CPUs must be > 0 (call ApplyDefaults first)")
	}
	if s.MemoryGB == 0 {
		return fmt.Errorf("MemoryGB must be > 0 (call ApplyDefaults first)")
	}
	return nil
}

// DeterministicMAC derives a stable locally-administered MAC address from a
// cell name. Same cell → same MAC → same DHCP lease, avoiding lease
// accumulation across VM restarts and reinits.
func DeterministicMAC(cellName string) string {
	h := sha256.Sum256([]byte("devcell-tart:" + cellName))
	mac := make(net.HardwareAddr, 6)
	copy(mac, h[:6])
	mac[0] = (mac[0] | 0x02) & 0xFE // locally administered, unicast
	return mac.String()
}

// TemplateDir returns the path to per-template VM artifacts.
// Layout: ~/.devcell/darwin/<stackTag>/
func TemplateDir(home, stack string, modules []string) string {
	return filepath.Join(home, ".devcell", "darwin", StackTag(stack, modules))
}

// ArtifactDir returns the path to VM artifacts for a given cell.
// Deprecated: use TemplateDir for per-template paths or CellHome for per-cell paths.
func ArtifactDir(home, cellName string) string {
	return filepath.Join(home, ".devcell", cellName, "darwin")
}
