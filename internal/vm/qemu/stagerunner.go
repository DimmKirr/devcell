package qemu

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunGuestStages executes a GuestStage table against a running guest over SSH.
//
// This is the one place that knows how to drive guest-side work: retries,
// reboots, stages whose own command tears down the session, per-stage
// deadlines, and component-grouped logs streamed to disk while they run.
// `cell build` and the dev-env test both call it, so what the test proves is
// what users get — the two had drifted into separate loops, and the CLI's was
// the weaker one (no reboots, no timeout, nothing readable until exit).
//
// The caller owns the VM. Rebooting and waiting for SSH are supplied as
// callbacks because only the caller knows how its VM is started and watched.
type StageRunOptions struct {
	// SSHUser and SSHKeyPath authenticate to the guest.
	SSHUser    string
	SSHKeyPath string
	// LogDir receives one log per component, named by StageLogNames. Empty
	// disables file logging (output is still returned to the observer).
	LogDir string
	// StageTimeout bounds a single stage. Zero means DefaultStageTimeout.
	StageTimeout time.Duration
	// Reboot restarts the guest and returns once it answers SSH again.
	// Required if any stage sets RebootAfter.
	Reboot func(ctx context.Context, reason string) error
	// BeforeStage and AfterStage let a caller checkpoint, screenshot or
	// annotate around a stage. Both are optional; an error from either fails
	// the run.
	BeforeStage func(ctx context.Context, idx int, stage GuestStage) error
	AfterStage  func(ctx context.Context, idx int, stage GuestStage, output string) error
	Observer    Observer
}

// NoChangeMarker is what a stage prints to say it changed nothing, so the
// runner can skip a reboot it does not need. A TCG reboot costs ~8 minutes
// (run 20260803T075624 paid exactly that after an engine stage that found
// the engine already installed).
const NoChangeMarker = "DEVCELL-NO-CHANGE"

// stageNeedsReboot reports whether the runner should reboot after a stage:
// the stage must ask for it AND must not have reported a no-op.
func stageNeedsReboot(stage GuestStage, output string) bool {
	if !stage.RebootAfter {
		return false
	}
	return !strings.Contains(output, NoChangeMarker)
}

// DefaultStageTimeout bounds a single stage. The slowest legitimate stage
// (nix or a driver install under TCG) runs well under an hour; a dev-env stage
// once sat wedged for three because nothing bounded it.
const DefaultStageTimeout = 90 * time.Minute

// RunGuestStages runs every stage in order, stopping at the first failure —
// stages are strictly dependent, so continuing only produces confusing
// downstream errors.
func RunGuestStages(ctx context.Context, spec Spec, stages []GuestStage, opts StageRunOptions) error {
	if opts.Observer == nil {
		opts.Observer = NopObserver{}
	}
	if opts.StageTimeout == 0 {
		opts.StageTimeout = DefaultStageTimeout
	}
	logNames := StageLogNames(stages)

	for i, stage := range stages {
		if opts.BeforeStage != nil {
			if err := opts.BeforeStage(ctx, i, stage); err != nil {
				return fmt.Errorf("before stage %q: %w", stage.Name, err)
			}
		}

		opts.Observer.Logf("stage [%d/%d] %s", i+1, len(stages), stage.Name)
		var logPath string
		if opts.LogDir != "" {
			logPath = filepath.Join(opts.LogDir, logNames[i])
		}
		appendStageMarker(logPath, "[start] stage %d/%d %q", i+1, len(stages), stage.Name)

		started := time.Now()
		out, err := runStageWithRetries(ctx, spec, stage, logPath, opts)
		if err != nil {
			appendStageMarker(logPath, "[end] stage %q FAILED in %s: %v",
				stage.Name, time.Since(started).Round(time.Second), err)
			return fmt.Errorf("stage %q failed: %w\n%s", stage.Name, err, tailOf(out, 20))
		}
		appendStageMarker(logPath, "[end] stage %q ok in %s",
			stage.Name, time.Since(started).Round(time.Second))

		if opts.AfterStage != nil {
			if err := opts.AfterStage(ctx, i, stage, out); err != nil {
				return fmt.Errorf("after stage %q: %w", stage.Name, err)
			}
		}

		if stageNeedsReboot(stage, out) {
			if opts.Reboot == nil {
				return fmt.Errorf("stage %q needs a reboot but no Reboot func was supplied", stage.Name)
			}
			if err := opts.Reboot(ctx, "after "+stage.Name); err != nil {
				return fmt.Errorf("reboot after %q: %w", stage.Name, err)
			}
		}
	}
	return nil
}

// timestampWriter prefixes every completed line with an ISO-8601 UTC
// instant. Stage logs are read to answer "where did the 30 minutes go" —
// durations between adjacent lines are the answer, and they only exist if
// each line is stamped. Partial writes are buffered so a line split across
// chunks is stamped once, when it completes.
type timestampWriter struct {
	w       io.Writer
	partial []byte
}

func (t *timestampWriter) Write(p []byte) (int, error) {
	n := len(p)
	buf := append(t.partial, p...)
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		line := buf[:i]
		buf = buf[i+1:]
		// The guest stamps its own lines (Write-DevcellLog). Stamping them
		// again puts two clocks on one line and the host's is the less
		// truthful of the two — it records arrival, not when the work ran.
		if isoStamped(line) {
			if _, err := fmt.Fprintf(t.w, "%s\n", line); err != nil {
				return n, err
			}
			continue
		}
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		if _, err := fmt.Fprintf(t.w, "%s %s\n", stamp, line); err != nil {
			return n, err
		}
	}
	t.partial = append([]byte(nil), buf...)
	return n, nil
}

// appendStageMarker writes a runner-side lifecycle line into a component
// log, stamped with wall-clock UTC. The guest's own transcript header says a
// stage began writing; these markers say when the RUNNER started and
// finished it — without them a mid-run tail cannot tell "still running"
// from "done, next stage quiet".
func appendStageMarker(logPath, format string, args ...interface{}) {
	if logPath == "" {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s === "+format+"\n",
		append([]interface{}{time.Now().UTC().Format("2006-01-02T15:04:05Z")}, args...)...)
}

// runStageWithRetries runs one stage, retrying as the stage allows. A stage
// that tolerates disconnects treats "closed by remote host" as success: its own
// command took the session down, and the next stage verifies the outcome.
func runStageWithRetries(ctx context.Context, spec Spec, stage GuestStage, logPath string, opts StageRunOptions) (string, error) {
	var out string
	var err error
	for attempt := 0; attempt <= stage.Retries; attempt++ {
		started := time.Now()
		out, err = runStageOnce(ctx, spec, opts.SSHUser, opts.SSHKeyPath, stagePayload(stage), logPath, opts.StageTimeout)
		took := time.Since(started).Round(time.Second)

		if err != nil && stage.ToleratesDisconnect && strings.Contains(out, "closed by remote host") {
			opts.Observer.Logf("stage %q dropped the SSH session as expected after %s", stage.Name, took)
			return out, nil
		}
		if err == nil {
			opts.Observer.Logf("stage %q ok in %s", stage.Name, took)
			return out, nil
		}
		opts.Observer.Logf("stage %q attempt %d/%d failed after %s: %v",
			stage.Name, attempt+1, stage.Retries+1, took, err)
		if attempt < stage.Retries {
			time.Sleep(5 * time.Second)
		}
	}
	return out, err
}

// runStageOnce runs a script in the guest, mirroring its output to logPath as
// it arrives so a long stage is observable with `tail -f` instead of revealing
// nothing until it exits, and killing it if it exceeds timeout.
func runStageOnce(ctx context.Context, spec Spec, sshUser, sshKeyPath, script, logPath string, timeout time.Duration) (string, error) {
	argv := BuildSSHExecArgv(spec.SSHHost, spec.SSHPort, sshUser, sshKeyPath, PowerShellEncodedCommand(script))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	var mem strings.Builder
	w := io.Writer(&mem)
	if logPath != "" {
		// Append: several stages share one component log, each contributing
		// its own section rather than truncating the last.
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			// The file copy is timestamped per line (see timestampWriter);
			// the in-memory copy stays raw so assertions match guest output.
			w = io.MultiWriter(&timestampWriter{w: f}, &mem)
		}
	}
	cmd.Stdout, cmd.Stderr = w, w

	if err := cmd.Start(); err != nil {
		return mem.String(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return mem.String(), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return mem.String(), fmt.Errorf("stage exceeded %s and was killed", timeout)
	}
}

func tailOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// isoStamped reports whether a line already begins with an ISO-8601 basic
// UTC instant ("2026-08-03T08:02:02Z ").
func isoStamped(line []byte) bool {
	const want = len("2026-08-03T08:02:02Z")
	if len(line) < want+1 || line[want] != ' ' {
		return false
	}
	for i, c := range line[:want] {
		switch i {
		case 4, 7:
			if c != '-' {
				return false
			}
		case 10:
			if c != 'T' {
				return false
			}
		case 13, 16:
			if c != ':' {
				return false
			}
		case 19:
			if c != 'Z' {
				return false
			}
		default:
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
