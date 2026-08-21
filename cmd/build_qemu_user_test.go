//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// The account the build connects to must be the account the answer file
// creates. autounattend.xml creates qemu.SessionUsername() — the host's $USER,
// mirroring HOST_USER in every other engine — and the guest bootstrap
// authorizes the SSH key for that account and no other.
//
// Hardcoding "devcell" here meant a build that installed Windows perfectly,
// reached SSH, and then failed every provisioning step with
//
//	devcell@127.0.0.1: Permission denied (publickey,password,keyboard-interactive)
//
// after a 2h47m install (run 20260730T222409).
func TestQemuBuildSSHUser_MatchesTheAccountTheAnswerFileCreates(t *testing.T) {
	t.Setenv("USER", "dmitry")

	if got, want := qemuBuildSSHUser(), qemu.SessionUsername(); got != want {
		t.Errorf("build connects as %q but the guest account is %q — provisioning cannot authenticate", got, want)
	}
}

// With no $USER to mirror, both sides must still agree — on the default.
func TestQemuBuildSSHUser_FallsBackToTheDefaultSessionUser(t *testing.T) {
	os.Unsetenv("USER")
	t.Setenv("USER", "")

	if got, want := qemuBuildSSHUser(), qemu.DefaultSessionUser; got != want {
		t.Errorf("with no $USER the build must connect as %q, got %q", want, got)
	}
}

// The firmware talks on the serial port and nowhere else. Every boot-level root
// cause found in this project came from that log — the cdboot stack overflow,
// "Image type X64 can't be loaded", the wrong-device boot that parked at
// "Start boot option". A build that does not capture it leaves the one class of
// failure it cannot otherwise explain completely invisible.
//
// The guest's own progress channel (virtio-serial) is the matching outbound path:
// it is the only way a guest with no network reports on itself while installing.
func TestQemuDiagnosticPaths_CaptureSerialAndGuestProgress(t *testing.T) {
	serial, guestProgress := qemuDiagnosticPaths("/project/.scratch/debug/20260101T000000Z")

	if serial == "" {
		t.Fatal("the build must capture firmware serial output")
	}
	if guestProgress == "" {
		t.Fatal("the build must capture the guest progress channel")
	}
	if serial == guestProgress {
		t.Errorf("the two channels must not share a file: %s", serial)
	}
	for _, p := range []string{serial, guestProgress} {
		if !strings.HasPrefix(p, "/project/.scratch/debug/") {
			t.Errorf("diagnostics belong under the debug directory, got %s", p)
		}
	}
}

// Which ports a build actually took is not cosmetic: the allocator walks past
// anything already bound, so the SSH port is 10022 only when nothing else holds
// it. When a build fails, "which port was this VM on?" decides whether you are
// looking at the right VM at all — and until now it appeared only inside a
// debug line about waiting for SSH.
func TestFormatAllocatedPorts_NamesEveryPortTheBuildTook(t *testing.T) {
	summary := formatAllocatedPorts(qemu.AllocatedPorts{
		SSHPort: "10023", VNCPort: "10050", RDPPort: "10089",
	})

	for _, want := range []string{"ssh=10023", "vnc=10050", "rdp=10089"} {
		if !strings.Contains(summary, want) {
			t.Errorf("port summary must state %s, got %q", want, summary)
		}
	}
}

// A build that renders nowhere is the right default for CI, but it makes a
// desktop user watch a 3-hour install through periodic screendumps. The runner
// already honours `[cell] qemu_display` / DEVCELL_QEMU_DISPLAY; the build
// hardcoded "none", so the same config meant two different things depending on
// which command you ran.
func TestQemuBuildDisplay_HonoursTheConfiguredDisplay(t *testing.T) {
	t.Setenv("DEVCELL_QEMU_DISPLAY", "gtk")

	if got := qemuBuildDisplay(cfg.CellSection{}); got != "gtk" {
		t.Errorf("build must honour the configured display, got %q", got)
	}
}

// Headless stays the default: most builds run without an X server, and a build
// that dies because it cannot open a window is worse than one you cannot watch.
func TestQemuBuildDisplay_DefaultsToHeadless(t *testing.T) {
	t.Setenv("DEVCELL_QEMU_DISPLAY", "")

	if got := qemuBuildDisplay(cfg.CellSection{}); got != "none" {
		t.Errorf("build must default to headless, got %q", got)
	}
}

// The build allocates an ssh/vnc/rdp trio but only ever set SSHPort on the
// spec, and command.go adds the 3389 forward only when RDPPort > 0. So the
// build reserved rdp=10089, printed it, and forwarded nothing — `cell rdp`
// against a build VM could not connect, while the guest's RDP service was
// running and answering (verified: TLS negotiated over a manual hostfwd_add).
func TestQemuBuildPorts_ForwardEveryPortTheyAllocate(t *testing.T) {
	ports := qemu.AllocatedPorts{SSHPort: "10022", VNCPort: "10050", RDPPort: "10089"}

	spec := qemuBuildSpecPorts(ports)

	if spec.SSHPort == 0 {
		t.Error("ssh must be forwarded")
	}
	if spec.RDPPort == 0 {
		t.Error("rdp must be forwarded — a reserved port nothing listens on is worse than none")
	}
	if spec.VNCPort == 0 {
		t.Error("vnc must be set so QEMU serves the console itself")
	}
}

// Keys lived at ~/.devcell/<cell>/qemu/, but they are not qemu's: libvirt reuses
// the same pair, and .ssh is where anyone looks first. The engine name in the
// path was an accident of which engine happened to need keys first.
func TestQemuKeyDir_PrefersDotSSH(t *testing.T) {
	home := t.TempDir()

	dir := qemuKeyDir(home, "DIMM")

	if want := filepath.Join(home, ".devcell", "DIMM", ".ssh"); dir != want {
		t.Errorf("new cells must use %s, got %s", want, dir)
	}
}

// A template already built has the old key baked into the guest. Moving the
// path must not orphan it — that would silently turn a 3-hour template into
// one nothing can log into.
func TestQemuKeyDir_KeepsUsingTheLegacyPathWhenAKeyIsAlreadyThere(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".devcell", "DIMM", "qemu")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "id_ed25519"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if dir := qemuKeyDir(home, "DIMM"); dir != legacy {
		t.Errorf("an existing key must keep its path, got %s", dir)
	}
}

// Every qemu template path passes modules=nil, so two cells on the same stack
// with different module sets resolve to one disk-base.qcow2 and one
// .provisioned marker. The first build wins; the second either reuses a
// template missing its modules or, with --force, destroys the first. StackTag
// exists precisely to keep them apart — tart passes modules, qemu does not.
func TestQemuTemplatePaths_SeparateTemplatesPerModuleSet(t *testing.T) {
	home := t.TempDir()

	bare := qemu.TemplateDir(home, "base", nil)
	withMods := qemu.TemplateDir(home, "base", []string{"docker", "node"})

	if bare == withMods {
		t.Fatalf("module sets must not share a template dir: both resolved to %s", bare)
	}
	if qemu.ImageName("base", nil) == qemu.ImageName("base", []string{"docker", "node"}) {
		t.Error("module sets must not share a disk image name")
	}
	if qemu.ProvisionedMarker(home, "base", nil) == qemu.ProvisionedMarker(home, "base", []string{"docker", "node"}) {
		t.Error("module sets must not share a provisioned marker")
	}
}

// Module order is not meaningful, so it must not fork the template.
func TestQemuTemplatePaths_ModuleOrderDoesNotMatter(t *testing.T) {
	home := t.TempDir()

	if a, b := qemu.TemplateDir(home, "base", []string{"node", "docker"}),
		qemu.TemplateDir(home, "base", []string{"docker", "node"}); a != b {
		t.Errorf("the same modules in a different order must be one template: %s vs %s", a, b)
	}
}

// Provisioning reports only pass/fail today, so a step that takes an hour and a
// step that fails instantly read the same in the log. Under TCG the difference
// between "slow" and "stuck" is the whole diagnosis, and on 2026-07-31 a step
// that had already FAILED was reported as "still running" for three hours.
func TestFormatProvisionStep_NamesStepAttemptAndDuration(t *testing.T) {
	line := formatProvisionStep("Install dev tools", 2, 3, 95*time.Second, nil)

	for _, want := range []string{"Install dev tools", "2/3", "1m35s"} {
		if !strings.Contains(line, want) {
			t.Errorf("step line must state %q, got %q", want, line)
		}
	}
}

// A failure must carry its cause on the same line — the reason a build failed
// should not require correlating two log entries.
func TestFormatProvisionStep_CarriesTheFailureCause(t *testing.T) {
	line := formatProvisionStep("Configure SSH", 1, 3, time.Second, errors.New("exit status 255"))

	if !strings.Contains(line, "exit status 255") {
		t.Errorf("a failed step must state its error, got %q", line)
	}
	if !strings.Contains(strings.ToUpper(line), "FAIL") {
		t.Errorf("a failed step must be visibly a failure, got %q", line)
	}
}
