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
	"sync"
	"syscall"
	"time"
)

// VMState represents the current state of a QEMU VM.
type VMState string

const (
	StateUnknown VMState = "unknown"
	StateStopped VMState = "stopped"
	StateRunning VMState = "running"
	StateError   VMState = "error"
)

// VM wraps a running QEMU process.
type VM struct {
	spec   Spec
	cmd    *exec.Cmd
	obs    Observer
	pidDir string // directory for qemu.pid file (empty = no PID file)

	mu     sync.Mutex
	state  VMState
	err    error
	output ringBuffer
}

// ringBuffer keeps the last N bytes written to it (QEMU stderr can be verbose).
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if r.max > 0 && len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// NewVM creates a VM handle. Call Start() to launch it.
// If pidDir is non-empty, Start() writes a PID file there and process exit cleans it up.
func NewVM(spec Spec, obs Observer, pidDir string) *VM {
	return &VM{
		spec:   spec,
		obs:    obs,
		pidDir: pidDir,
		state:  StateStopped,
		output: ringBuffer{max: 8192},
	}
}

// QMPSockPath returns the QMP unix socket path for this VM.
func (v *VM) QMPSockPath() string {
	return QMPSocketPath(v.spec)
}

// State returns the current VM state.
func (v *VM) State() VMState {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state
}

// StateString returns the VM state as a string (for VMStateFunc compatibility).
func (v *VM) StateString() string {
	return string(v.State())
}

// LastOutput returns the tail of QEMU's captured stdout+stderr.
func (v *VM) LastOutput() string {
	return strings.TrimSpace(v.output.String())
}

// ExitError returns the error from QEMU process exit (nil if still running or clean exit).
func (v *VM) ExitError() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

// observerWriter wraps Observer.Logf as an io.Writer — each line is logged.
type observerWriter struct {
	obs    Observer
	prefix string
	buf    bytes.Buffer
}

func (w *observerWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			w.obs.Logf("%s%s", w.prefix, line)
		}
	}
	return len(p), nil
}

// Start launches the QEMU process in the background.
func (v *VM) Start(ctx context.Context) error {
	v.mu.Lock()
	if v.state == StateRunning {
		v.mu.Unlock()
		return fmt.Errorf("VM already running")
	}
	v.mu.Unlock()

	argv := BuildRunCommand(v.spec)
	v.obs.Logf("starting QEMU: %s", strings.Join(argv, " "))

	v.cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	v.setupOutput()

	if err := v.cmd.Start(); err != nil {
		v.mu.Lock()
		v.state = StateError
		v.err = err
		v.mu.Unlock()
		return fmt.Errorf("starting QEMU: %w", err)
	}

	v.obs.Logf("QEMU started (PID %d)", v.cmd.Process.Pid)

	if v.pidDir != "" {
		if err := WritePIDFile(v.pidDir, v.cmd.Process.Pid); err != nil {
			v.obs.Logf("warning: failed to write PID file: %v", err)
		}
	}

	v.mu.Lock()
	v.state = StateRunning
	v.mu.Unlock()

	// Monitor the process in background
	go v.waitForProcess()

	return nil
}

// StartInstall launches the QEMU process for Windows installation.
func (v *VM) StartInstall(ctx context.Context, windowsISO, autounattendISO string) error {
	v.mu.Lock()
	if v.state == StateRunning {
		v.mu.Unlock()
		return fmt.Errorf("VM already running")
	}
	v.mu.Unlock()

	argv := BuildInstallCommand(v.spec, windowsISO, autounattendISO)
	v.obs.Logf("starting QEMU install: %s", strings.Join(argv, " "))

	v.cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	v.setupOutput()

	if err := v.cmd.Start(); err != nil {
		v.mu.Lock()
		v.state = StateError
		v.err = err
		v.mu.Unlock()
		return fmt.Errorf("starting QEMU install: %w", err)
	}

	v.obs.Logf("QEMU install started (PID %d)", v.cmd.Process.Pid)

	v.mu.Lock()
	v.state = StateRunning
	v.mu.Unlock()

	go v.waitForProcess()

	return nil
}

// setupOutput wires stdout and stderr to both the ring buffer and the observer.
func (v *VM) setupOutput() {
	stdoutLog := &observerWriter{obs: v.obs, prefix: "qemu: "}
	stderrLog := &observerWriter{obs: v.obs, prefix: "qemu/err: "}
	v.cmd.Stdout = io.MultiWriter(&v.output, stdoutLog)
	v.cmd.Stderr = io.MultiWriter(&v.output, stderrLog)
}

// waitForProcess monitors the QEMU process and updates state on exit.
func (v *VM) waitForProcess() {
	err := v.cmd.Wait()
	if v.pidDir != "" {
		os.Remove(filepath.Join(v.pidDir, pidFileName))
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		v.state = StateError
		v.err = err
		v.obs.Logf("QEMU exited with error: %v", err)
		if out := v.output.String(); out != "" {
			last := out
			if len(last) > 500 {
				last = last[len(last)-500:]
			}
			v.obs.Logf("QEMU last output:\n%s", strings.TrimSpace(last))
		}
	} else {
		v.state = StateStopped
		v.obs.Logf("QEMU exited cleanly")
	}
}

// Shutdown sends ACPI powerdown via QMP, then waits for the process to exit.
func (v *VM) Shutdown(ctx context.Context) error {
	v.mu.Lock()
	if v.state != StateRunning || v.cmd == nil || v.cmd.Process == nil {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	v.obs.Logf("sending SIGTERM to QEMU (PID %d)", v.cmd.Process.Pid)
	if err := v.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		v.obs.Logf("SIGTERM failed: %v, trying kill", err)
		return v.ForceStop()
	}

	done := make(chan struct{})
	go func() {
		for {
			if v.State() != StateRunning {
				close(done)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	select {
	case <-done:
		v.obs.Logf("QEMU shutdown complete")
		return nil
	case <-ctx.Done():
		v.obs.Logf("shutdown timed out, force stopping")
		return v.ForceStop()
	}
}

// ForceStop kills the QEMU process immediately.
func (v *VM) ForceStop() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cmd == nil || v.cmd.Process == nil {
		return nil
	}
	return v.cmd.Process.Kill()
}

// WaitForExit blocks until the VM process exits.
func (v *VM) WaitForExit(ctx context.Context) error {
	for {
		if v.State() != StateRunning {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
