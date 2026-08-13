package qemu

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Spec holds everything needed to configure and connect to a QEMU Windows VM.
type Spec struct {
	VMName       string
	CPUs         uint
	MemoryGB     uint64
	DiskPath     string // path to qcow2 disk
	FirmwarePath string // path to EDK2 UEFI firmware
	VarsPath     string // path to UEFI variable store (per-VM copy)
	VirtioISO    string // path to VirtIO drivers ISO
	SSHPort      uint16 // forwarded port for SSH
	VNCPort      uint16 // forwarded port for VNC (0 = disabled)
	RDPPort      uint16 // forwarded port for RDP (0 = disabled)
	SSHHost      string // SSH host (default "127.0.0.1")
	SSHUser      string // guest username (defaults to the host $USER)
	SSHKeyPath   string // path to SSH private key
	MACAddr      string // deterministic MAC address
	Binary       string // agent binary (e.g. "claude", "cmd.exe")
	DefaultFlags []string
	UserArgs     []string
	EnvVars      []string // KEY=VALUE pairs
	ProjectDir   string   // host project directory
	DisplayType  string   // "none", "cocoa", "sdl" (default "none")
	QMPSocketDir string   // directory for QMP socket; defaults to /tmp

	// Accel overrides the QEMU accelerator (e.g. "tcg,thread=multi" to force
	// software emulation). Empty means ApplyDefaults resolves it — see
	// ResolveAccel.
	Accel string
	// KVM carries the `[cell] kvm` config intent: the launcher was asked to
	// pass /dev/kvm into the container. It is intent only — ApplyDefaults still
	// probes the device before selecting KVM.
	KVM bool
	// AccelReason explains the resolved Accel in one line, for the launch log.
	// Set by ApplyDefaults; never an input.
	AccelReason string
	// CPU overrides the -cpu string (e.g. "max,pauth-impdef=on,pmu=off").
	// Empty means cpuType picks the per-accelerator default. Exists so a test
	// can vary exactly one CPU feature against an otherwise identical machine
	// — the mechanism behind the PMU evidence table in boot_test.go.
	CPU string
	// SerialLogPath, when set, redirects the guest serial console to this file.
	SerialLogPath string
	// GuestProgressLogPath, when set, attaches a virtio-serial port
	// (ProgressPortName) wired to this file. The guest writes progress via
	// \\.\Global\<ProgressPortName>; this works on ARM64 where pci-serial
	// 16550 devices don't map to user-mode COMx (CELL-430).
	GuestProgressLogPath string
	// DiskCacheMode sets the qcow2 cache policy (e.g. "unsafe" to drop guest
	// flushes). Empty keeps QEMU's safe default. "unsafe" makes the image
	// worthless if the host dies mid-run, so it is only for throwaway VMs
	// such as the install test, where it removes a large TCG cost.
	DiskCacheMode string
	// NoReboot stops the VM instead of rebooting it — useful when a reboot
	// marks the end of a phase you want to observe.
	NoReboot bool
	// GuestAgentSocketPath, when set, attaches a virtio-serial port named
	// org.qemu.guest_agent.0 (the name qemu-ga looks for by convention) wired
	// to a host unix socket. On ARM64 the agent binary is the x64 MSI running
	// under Win11's emulation (no native build exists — see VIRTIO.md), but
	// the channel wiring is the standard one.
	GuestAgentSocketPath string
	// VirtioFSSocketPath + VirtioFSTag, when both set, attach a
	// vhost-user-fs device backed by a virtiofsd socket. The tag is what the
	// guest mounts by. Requires shareable guest RAM (memory-backend-memfd),
	// which BuildRunCommand adds alongside the device.
	VirtioFSSocketPath string
	VirtioFSTag        string
	// NestedVirt asks for the machine features Windows' own hypervisor needs
	// in order to launch inside the guest: EL2 plus a GICv3 with ITS and a
	// secure world. Opt-in, because it changes the boot environment of an
	// already-installed Windows and the install path is proven without it.
	NestedVirt bool
	// SecureWorld adds secure=on: Arm Security Extensions (TrustZone), which
	// gives the machine an EL3 and a secure flash bank. The firmware in
	// pflash then *becomes* the secure-world firmware and is entered at EL3 —
	// a normal-world EDK2 build cannot do that job. Separate from NestedVirt
	// so the two can be tested apart.
	SecureWorld bool
	// FirmwareKernel loads the UEFI firmware with -kernel instead of pflash.
	// Under secure=on this is what lets a stock, normal-world EDK2 work:
	// QEMU's own ARM boot stub takes the EL3 entry and drops the payload to
	// non-secure. Firmware-in-pflash has no such stub, so the same binary is
	// entered at EL3 and hangs. Costs the pflash NVRAM store.
	FirmwareKernel bool
	// DiskBus selects the system disk controller: "nvme" (default, inbox
	// driver, what our installs use) or "scsi" (virtio-scsi + scsi-hd, what
	// the proven Hyper-V config uses).
	DiskBus string
	// CDBus selects the CD/ISO attachment: "usb" (default, usb-storage on
	// xhci — inbox USBSTOR, no extra driver) or "scsi" (scsi-cd on a
	// dedicated virtio-scsi-pci controller — needs vioscsi drvload in WinPE).
	CDBus string
	// MachineType overrides the -machine string (e.g. "virt,highmem=on").
	// Empty means machineType() picks the per-accelerator default.
	MachineType string
	// LogVolumePath, when set, attaches a raw FAT image as removable USB
	// storage for guest-written logs — the run-time counterpart of the
	// install's answer volume (see BuildDevEnvLogVolume).
	LogVolumePath string
}

// usesTCG reports whether this spec runs under software emulation.
func (s *Spec) usesTCG() bool {
	return strings.HasPrefix(s.effectiveAccel(), "tcg")
}

func (s *Spec) effectiveAccel() string {
	if s.Accel != "" {
		return s.Accel
	}
	return accelerator(runtime.GOOS)
}

// maxUnixSocketPath is the kernel's limit on an AF_UNIX path: sun_path is 108
// bytes on Linux and 104 on macOS. Take the smaller so a path is portable, and
// note QEMU rejects an over-long path outright ("Path must be less than 108
// bytes") rather than truncating it — the VM never starts.
const maxUnixSocketPath = 104

// QMPSocketPath returns the path to the QMP unix socket for a given spec.
//
// The socket normally lives next to the VM it belongs to, but that directory is
// the caller's choice and can be arbitrarily deep — `cell build` nests it four
// levels under $HOME, which overflows sun_path for an ordinary home path. When
// the natural path does not fit, fall back to a short one in the system temp
// directory, keyed by a digest of the original so the result stays stable and
// unique per VM: `cell rdp`/`cell vnc` find a running VM by recomputing this
// path, so it must be a pure function of the spec.
func QMPSocketPath(spec Spec) string {
	dir := spec.QMPSocketDir
	if dir == "" {
		dir = "/tmp"
	}
	natural := filepath.Join(dir, "qemu-"+spec.VMName+"-qmp.sock")
	if len(natural) < maxUnixSocketPath {
		return natural
	}
	sum := sha256.Sum256([]byte(natural))
	return filepath.Join(os.TempDir(), fmt.Sprintf("devcell-qmp-%x.sock", sum[:8]))
}

// DefaultSSHPort is the *preferred* forwarded SSH port, not a fixed one.
// ApplyDefaults falls back to a kernel-assigned free port when it is taken.
//
// It used to be assigned unconditionally, so two overlapping VMs raced for the
// same hostfwd and the loser aborted at launch with
//
//	hostfwd=tcp:127.0.0.1:2222-:22: Could not set up host forwarding rule
//
// which surfaced downstream only as "QMP socket did not appear within 30s" and
// an empty results directory — see TestSpec_ApplyDefaults_SkipsAPortAlreadyInUse.
const DefaultSSHPort = 2222

// freeTCPPort returns pref when it is bindable right now, otherwise a port the
// kernel reports as free. Asking the kernel is what makes this safe across
// processes: it never hands back a port another QEMU is still holding, which a
// fixed constant cannot promise.
//
// The listener is closed before QEMU binds it, so a concurrent allocator could
// in principle claim it in the gap. That window is far smaller than the
// guaranteed collision a constant produced, and the hostfwd error names the
// port when it does happen.
func freeTCPPort(pref uint16) uint16 {
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", pref)); err == nil {
		ln.Close()
		return pref
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return pref // nothing better to offer; let QEMU report the bind failure
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
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
		s.SSHPort = freeTCPPort(DefaultSSHPort)
	}
	if s.SSHHost == "" {
		s.SSHHost = "127.0.0.1"
	}
	if s.SSHUser == "" {
		// Must match the account the answer file creates — see SessionUsername.
		s.SSHUser = SessionUsername()
	}
	if s.DisplayType == "" {
		s.DisplayType = "none"
	}
	s.resolveAccel(runtime.GOOS, ProbeKVM)
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
