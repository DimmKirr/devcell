package qemu

import (
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
)

// Spec holds everything needed to configure and connect to a QEMU Windows VM.
type Spec struct {
	VMName       string
	CPUs         uint
	MemoryGB     uint64
	DiskPath     string            // path to qcow2 disk
	FirmwarePath string            // path to EDK2 UEFI firmware
	VarsPath     string            // path to UEFI variable store (per-VM copy)
	VirtioISO    string            // path to VirtIO drivers ISO
	SharedDirs   map[string]string // tag -> host path (virtiofs)
	SSHPort      uint16            // forwarded port for SSH
	VNCPort      uint16            // forwarded port for VNC (0 = disabled)
	RDPPort      uint16            // forwarded port for RDP (0 = disabled)
	SSHHost      string            // SSH host (default "127.0.0.1")
	SSHUser      string            // guest username (default "devcell")
	SSHKeyPath   string            // path to SSH private key
	MACAddr      string            // deterministic MAC address
	Binary       string            // agent binary (e.g. "claude", "cmd.exe")
	DefaultFlags []string
	UserArgs     []string
	EnvVars      []string // KEY=VALUE pairs
	ProjectDir   string   // host project directory
	DisplayType  string // "none", "cocoa", "sdl" (default "none")
	QMPSocketDir string // directory for QMP socket; defaults to /tmp
}

// QMPSocketPath returns the path to the QMP unix socket for a given spec.
func QMPSocketPath(spec Spec) string {
	dir := spec.QMPSocketDir
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "qemu-"+spec.VMName+"-qmp.sock")
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
		s.SSHPort = 2222
	}
	if s.SSHHost == "" {
		s.SSHHost = "127.0.0.1"
	}
	if s.SSHUser == "" {
		s.SSHUser = "devcell"
	}
	if s.DisplayType == "" {
		s.DisplayType = "none"
	}
}

// Validate returns an error if required fields are missing.
func (s *Spec) Validate() error {
	if s.DiskPath == "" {
		return fmt.Errorf("DiskPath is required")
	}
	if s.FirmwarePath == "" {
		return fmt.Errorf("FirmwarePath is required")
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
// cell name. Same cell -> same MAC -> same DHCP lease.
func DeterministicMAC(cellName string) string {
	h := sha256.Sum256([]byte("devcell-qemu:" + cellName))
	mac := make(net.HardwareAddr, 6)
	copy(mac, h[:6])
	mac[0] = (mac[0] | 0x02) & 0xFE // locally administered, unicast
	return mac.String()
}

// StackTag returns the canonical tag for a stack + optional modules.
func StackTag(stack string, modules []string) string {
	if len(modules) == 0 {
		return stack
	}
	sorted := make([]string, len(modules))
	copy(sorted, modules)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	sha8 := fmt.Sprintf("%x", h[:4])
	return fmt.Sprintf("%s-%s-%s", stack, strings.Join(sorted, "-"), sha8)
}

// TemplateDir returns the path to per-template VM artifacts.
// Layout: ~/.devcell/windows/<stackTag>/
func TemplateDir(home, stack string, modules []string) string {
	return filepath.Join(home, ".devcell", "windows", StackTag(stack, modules))
}

// InstanceDir returns the per-cell instance directory for Windows VMs.
// Layout: ~/.devcell/<cellName>/windows/
func InstanceDir(home, cellName string) string {
	return filepath.Join(home, ".devcell", cellName, "windows")
}

// TemplateVMName returns the QEMU VM name for a built template.
func TemplateVMName(stack string, modules []string) string {
	return "devcell-qemu-" + StackTag(stack, modules)
}

// InstanceVMName returns the QEMU VM name for a running cell instance.
func InstanceVMName(cellName string) string {
	return cellName + "-qemu"
}

// ImageName returns the disk image filename for a stack.
func ImageName(stack string, modules []string) string {
	return fmt.Sprintf("disk-%s.qcow2", StackTag(stack, modules))
}

// ProvisionedMarker returns the path to the ".provisioned" marker file
// within a template directory.
func ProvisionedMarker(home, stack string, modules []string) string {
	return filepath.Join(TemplateDir(home, stack, modules), ".provisioned")
}
