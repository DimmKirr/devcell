package qemu

import (
	"errors"
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"

	"github.com/devcell-sh/go-winkit/isokit"
)

// GuestLog is one file the guest wrote to the answer volume, or the reason it
// could not be read. A read failure is kept rather than discarded: "Setup never
// created a Panther directory" is usually the finding itself.
type GuestLog struct {
	Name    string
	Content []byte
	Err     error
}

var errNoSuchGuestLog = errors.New("not written by the guest")

// guestLogNames is the contract with the guest side. WinPE's agent writes the
// first three (Setup's own logs plus its result file); the first-logon
// bootstrap writes the last two. Order is chronological, so a dump reads as the
// install's own timeline.
var guestLogNames = []string{
	winpe.SetupActSnapshotName,
	winpe.SetupErrSnapshotName,
	winpe.AgentResultFile,
	unattend.BootstrapLogName,
	unattend.GuestDiagnosticsLogName,
}

// CollectGuestLogs reads every log the guest may have written to the answer
// volume. It always returns one entry per known log so the caller can report
// absence as clearly as content.
//
// This is the only channel that survives a guest with no network: the volume is
// plain FAT and the host reads it directly off the image file.
func CollectGuestLogs(answerImagePath string) []GuestLog {
	logs := make([]GuestLog, 0, len(guestLogNames))
	for _, name := range guestLogNames {
		data, err := isokit.ReadFileFromFAT(answerImagePath, "/"+name)
		if err != nil {
			logs = append(logs, GuestLog{Name: name, Err: fmt.Errorf("%w: %v", errNoSuchGuestLog, err)})
			continue
		}
		logs = append(logs, GuestLog{Name: name, Content: data})
	}
	return logs
}

// FormatGuestLogs renders collected logs for a terminal: each log under its own
// banner, and each absent log stated in words.
func FormatGuestLogs(logs []GuestLog) string {
	var b strings.Builder
	for _, l := range logs {
		if l.Err != nil {
			fmt.Fprintf(&b, "=== %s: not written by the guest ===\n", l.Name)
			continue
		}
		fmt.Fprintf(&b, "=== %s (%d bytes) ===\n%s\n", l.Name, len(l.Content), strings.TrimRight(string(l.Content), "\r\n"))
	}
	return b.String()
}

// BootstrapSteps is the guest's first-logon provisioning, read back from its own
// transcript.
//
// Invoke-Step catches exceptions and continues, so a failed step does not stop
// the bootstrap — it leaves a gap (no sshd, no key, no firewall rule) that only
// shows up later as a connection refused. Reading the transcript as one blob
// hides that; this splits it into what the guest actually reported.
type BootstrapSteps struct {
	OK         []string
	Failed     []string
	Unfinished []string // started, never reported — the guest died mid-step
}

const bootstrapPrefix = "devcell-bootstrap: "

// ParseBootstrapSteps reads step outcomes out of a bootstrap transcript.
func ParseBootstrapSteps(transcript string) BootstrapSteps {
	steps := BootstrapSteps{OK: []string{}, Failed: []string{}, Unfinished: []string{}}
	started := map[string]bool{}
	var order []string

	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, bootstrapPrefix)
		if i < 0 {
			continue
		}
		msg := line[i+len(bootstrapPrefix):]
		switch {
		case strings.HasPrefix(msg, "step: "):
			name := strings.TrimPrefix(msg, "step: ")
			if !started[name] {
				started[name] = true
				order = append(order, name)
			}
		case strings.HasPrefix(msg, "ok: "):
			name := strings.TrimPrefix(msg, "ok: ")
			steps.OK = append(steps.OK, name)
			delete(started, name)
		case strings.HasPrefix(msg, "FAILED: "):
			detail := strings.TrimPrefix(msg, "FAILED: ")
			steps.Failed = append(steps.Failed, detail)
			// The failure text is "<name> -- <message>"; clear by name.
			name, _, _ := strings.Cut(detail, " -- ")
			delete(started, name)
		}
	}

	for _, name := range order {
		if started[name] {
			steps.Unfinished = append(steps.Unfinished, name)
		}
	}
	return steps
}

// SSHReady reports whether the guest got far enough for the build to talk to
// it: sshd installed AND started. Installing the capability is not enough —
// that was the state of a guest that looked provisioned and refused every
// connection.
func (s BootstrapSteps) SSHReady() bool {
	var installed, started bool
	for _, name := range s.OK {
		if strings.Contains(name, "install OpenSSH server") {
			installed = true
		}
		if strings.Contains(name, "start sshd") {
			started = true
		}
	}
	return installed && started
}
