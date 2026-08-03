//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// runtimeGOOS is a variable so tests can resolve a budget for the other
// platform without cross-compiling.
var runtimeGOOS = runtime.GOOS

// installStallWindow is how long the guest may write nothing before the build
// calls it dead. Generous on purpose: under TCG Windows goes quiet for minutes
// at a time between phases, and a false stall throws away a real install. Ten
// times any pause observed in a healthy run.
const installStallWindow = 20 * time.Minute

// qemuDiagnosticPaths returns where the build records the two channels a guest
// can talk on before it has a network: the firmware's serial console and the
// guest's own pci-serial progress port.
//
// Both live beside the screenshots in the project's debug directory, so
// everything about a failed build is in one place.
func qemuDiagnosticPaths(debugDir string) (serial, guestProgress string) {
	return filepath.Join(debugDir, "serial.log"), filepath.Join(debugDir, "guest-progress.log")
}

// formatAllocatedPorts renders the ports a build took.
//
// The allocator starts from the preferred port and walks past anything already
// bound, so these are a result, not a constant — and every later question
// ("why did SSH not answer?", "which VM is on 10023?") starts from knowing
// them.
func formatAllocatedPorts(p qemu.AllocatedPorts) string {
	return fmt.Sprintf("ssh=%s vnc=%s rdp=%s", p.SSHPort, p.VNCPort, p.RDPPort)
}

// qemuBuildDisplay is the QEMU display backend the build renders to.
//
// Headless by default — most builds run without an X server, and one that dies
// because it cannot open a window is worse than one nobody watches. But when a
// display is configured, honour it: `cell shell` already does, and the same
// setting meaning two different things depending on the subcommand is a trap.
// With DEVCELL_QEMU_DISPLAY=gtk the install is a real window with working
// keyboard and mouse, instead of a series of screendumps.
func qemuBuildDisplay(cellCfg cfg.CellSection) string {
	return cellCfg.ResolvedQemuDisplay()
}

// qemuBuildSpecPorts puts every allocated port on the spec.
//
// Allocating a port and not forwarding it is worse than not allocating it: the
// build printed rdp=10089, reserved it against other cells, and forwarded
// nothing, so `cell rdp` failed against a guest whose RDP service was running
// fine. command.go only adds the 3389 forward when RDPPort is set.
func qemuBuildSpecPorts(ports qemu.AllocatedPorts) qemu.Spec {
	return qemu.Spec{
		SSHPort: ports.SSHPortUint16(),
		VNCPort: ports.VNCPortUint16(),
		RDPPort: ports.RDPPortUint16(),
	}
}

// qemuKeyDir is where a cell's VM SSH keypair lives.
//
// ~/.devcell/<cell>/.ssh — per cell, and engine-neutral: libvirt boots the same
// templates with the same keys, so naming the directory after qemu was an
// accident of which engine needed keys first.
//
// A cell that already has a key under the legacy qemu/ path keeps it. The
// public half is baked into a built template, so relocating the private half
// would leave a multi-hour template nothing can log into.
func qemuKeyDir(home, cellName string) string {
	legacy := filepath.Join(home, ".devcell", cellName, "qemu")
	if _, err := os.Stat(filepath.Join(legacy, "id_ed25519")); err == nil {
		return legacy
	}
	return filepath.Join(home, ".devcell", cellName, ".ssh")
}

// formatProvisionStep renders one provisioning attempt.
//
// Pass/fail alone cannot distinguish a slow step from a stuck one, and under
// TCG that is the whole diagnosis: `Add-WindowsCapability` legitimately runs
// for over an hour while emitting nothing. The duration is what tells an
// operator whether to wait or intervene, and a failure carries its cause on the
// same line so the reason never has to be correlated across entries.
func formatProvisionStep(name string, attempt, attempts int, took time.Duration, err error) string {
	status := "ok"
	if err != nil {
		status = "FAILED: " + err.Error()
	}
	return fmt.Sprintf("provision %s [%d/%d] %s — %s",
		name, attempt, attempts, took.Round(time.Second), status)
}

// qemuBuildSSHUser is the guest account the build authenticates as.
//
// It must track the answer file, which creates qemu.SessionUsername(): the
// guest bootstrap writes the SSH key into that account's authorized_keys (and
// the administrators file it belongs to), so any other name is a guaranteed
// publickey rejection.
func qemuBuildSSHUser() string {
	return qemu.SessionUsername()
}

// dumpGuestLogs prints everything the guest wrote to the answer volume.
//
// When the install fails, the guest usually cannot talk to us — no network, no
// SSH, no agent — and the FAT answer volume is the only channel left. Printing
// it here means a failed `cell build` explains itself on stdout instead of
// leaving an image file for someone to mount by hand.
func dumpGuestLogs(answerImagePath string) {
	logs := qemu.CollectGuestLogs(answerImagePath)
	fmt.Printf("\n%s\n", ux.StyleSection.Render(" Guest logs (read from the answer volume)"))
	fmt.Print(qemu.FormatGuestLogs(logs))
}

// qemuBuildResources is what the accelerator choice implies for the rest of the
// build: how much guest RAM to give it and how long to wait for the guest to
// answer SSH.
type qemuBuildResources struct {
	Accel       string
	AccelReason string
	MemoryGB    uint64
	SSHDeadline time.Duration
}

// Hardware-virtualization budget. A Windows install under HVF/KVM finishes in
// 20-40 minutes and 4 GB is comfortable.
const (
	acceleratedMemoryGB    = 4
	acceleratedSSHDeadline = 45 * time.Minute
)

// Software-emulation budget. TCG runs roughly 20x slower: a full install
// measured 2h42m in this project's own test runs, so a 45-minute deadline
// expires while Setup is still applying the image. Memory is 6 GB rather than
// 4 because QEMU's RSS under TCG runs well past guest RAM (translation buffers
// plus block cache) — an 8 GB guest met the OOM killer on a shared host, and
// 4 GB starves the install itself.
const (
	emulatedMemoryGB    = 6
	emulatedSSHDeadline = 5 * time.Hour
)

// qemuBuildBudget resolves the accelerator and scales the build to it.
//
// The accelerator is not a detail the rest of the build can ignore: the same
// install is a 30-minute job on HVF and a 3-hour job under TCG, and a plan that
// quotes hardware numbers while running emulated fails at the SSH wait with no
// hint that the deadline, not the guest, was wrong.
func qemuBuildBudget(cellCfg cfg.CellSection) qemuBuildResources {
	accel, reason := qemu.ResolveAccel("", cellCfg.ResolvedKVM(), runtimeGOOS, qemu.ProbeKVM)
	r := qemuBuildResources{
		Accel:       accel,
		AccelReason: reason,
		MemoryGB:    acceleratedMemoryGB,
		SSHDeadline: acceleratedSSHDeadline,
	}
	if strings.HasPrefix(accel, "tcg") {
		r.MemoryGB = emulatedMemoryGB
		r.SSHDeadline = emulatedSSHDeadline
		// Windows has a large code footprint and TCG re-translates whatever
		// falls out of its 32MB default cache — pure waste on a multi-hour
		// install. Measured worth it only under emulation.
		r.Accel = accel + ",tb-size=512"
	}
	return r
}
