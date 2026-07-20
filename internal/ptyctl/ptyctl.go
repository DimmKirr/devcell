package ptyctl

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

type Terminal struct {
	pty  xpty.Pty
	emu  *vt.SafeEmulator
	cmd  *exec.Cmd
	done chan error
	mu   sync.Mutex

	outMu  sync.Mutex
	outBuf []byte
}

func Start(ctx context.Context, name string, args ...string) (*Terminal, error) {
	const (
		width  = 120
		height = 40
	)

	pty, err := xpty.NewPty(width, height)
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}

	emu := vt.NewSafeEmulator(width, height)

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	if err := pty.Start(cmd); err != nil {
		pty.Close()
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	t := &Terminal{
		pty:  pty,
		emu:  emu,
		cmd:  cmd,
		done: make(chan error, 1),
	}

	// pump pty output → emulator + output buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				emu.Write(buf[:n])
				t.outMu.Lock()
				t.outBuf = append(t.outBuf, buf[:n]...)
				t.outMu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// pump emulator responses → pty input (terminal query answers)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				pty.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// wait for process exit
	go func() {
		t.done <- xpty.WaitProcess(ctx, cmd)
	}()

	return t, nil
}

// Screen returns the current text content of the virtual terminal.
func (t *Terminal) Screen() string {
	return t.emu.String()
}

// Send writes raw bytes to the PTY (stdin of the child process).
func (t *Terminal) Send(s string) error {
	_, err := t.pty.Write([]byte(s))
	return err
}

// SendLine sends text followed by Enter.
func (t *Terminal) SendLine(s string) error {
	return t.Send(s + "\r")
}

// Enter sends the Enter key.
func (t *Terminal) Enter() error {
	return t.Send("\r")
}

// WaitFor polls the screen until it contains the given substring or the
// context expires. Returns the screen content at time of match.
func (t *Terminal) WaitFor(ctx context.Context, substr string) (string, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return t.Screen(), fmt.Errorf("timed out waiting for %q; screen:\n%s", substr, t.Screen())
		case <-ticker.C:
			s := t.Screen()
			if strings.Contains(s, substr) {
				return s, nil
			}
		}
	}
}

// WaitForAny polls the screen until any of the given substrings appears.
// Returns the matching substring and the screen content.
func (t *Terminal) WaitForAny(ctx context.Context, substrs ...string) (matched string, screen string, err error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", t.Screen(), fmt.Errorf("timed out waiting for any of %v; screen:\n%s", substrs, t.Screen())
		case <-ticker.C:
			s := t.Screen()
			for _, sub := range substrs {
				if strings.Contains(s, sub) {
					return sub, s, nil
				}
			}
		}
	}
}

// Close kills the child process and releases the PTY.
func (t *Terminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}
	t.emu.Close()
	return t.pty.Close()
}

// Done returns a channel that receives the process exit error.
func (t *Terminal) Done() <-chan error {
	return t.done
}

// WriteStdin writes to the emulator's input pipe (keyboard events processed
// through the terminal emulator layer). For raw PTY writes, use Send().
func (t *Terminal) WriteStdin(p []byte) (int, error) {
	return t.emu.InputPipe().Write(p)
}

// Resize changes the terminal dimensions.
func (t *Terminal) Resize(w, h int) error {
	t.emu.Resize(w, h)
	return t.pty.Resize(w, h)
}

// Pid returns the PID of the child process, or -1 if not started.
func (t *Terminal) Pid() int {
	if t.cmd.Process == nil {
		return -1
	}
	return t.cmd.Process.Pid
}

// Render returns the screen content with ANSI escape codes intact.
func (t *Terminal) Render() string {
	return t.emu.Render()
}

// ReadOutput provides raw access to PTY output, bypassing the emulator.
// Most callers should use Screen() instead.
func (t *Terminal) ReadOutput(p []byte) (int, error) {
	return t.pty.Read(p)
}

// Cmd returns the underlying exec.Cmd for env/dir configuration before Start.
// Only useful when building a Terminal manually; Start() already configures it.
func (t *Terminal) Cmd() *exec.Cmd {
	return t.cmd
}

// WithEnv returns a StartOption that adds environment variables.
type StartOption func(*startConfig)

type startConfig struct {
	env    []string
	dir    string
	width  int
	height int
}

func WithEnv(env ...string) StartOption {
	return func(c *startConfig) {
		c.env = append(c.env, env...)
	}
}

func WithDir(dir string) StartOption {
	return func(c *startConfig) {
		c.dir = dir
	}
}

func WithSize(width, height int) StartOption {
	return func(c *startConfig) {
		c.width = width
		c.height = height
	}
}

func StartWithOptions(ctx context.Context, opts []StartOption, name string, args ...string) (*Terminal, error) {
	cfg := &startConfig{width: 120, height: 40}
	for _, o := range opts {
		o(cfg)
	}

	width, height := cfg.width, cfg.height

	pty, err := xpty.NewPty(width, height)
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}

	emu := vt.NewSafeEmulator(width, height)

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, cfg.env...)
	if cfg.dir != "" {
		cmd.Dir = cfg.dir
	}
	if err := pty.Start(cmd); err != nil {
		pty.Close()
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	t := &Terminal{
		pty:  pty,
		emu:  emu,
		cmd:  cmd,
		done: make(chan error, 1),
	}

	// pump pty output → emulator + output buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				emu.Write(buf[:n])
				t.outMu.Lock()
				t.outBuf = append(t.outBuf, buf[:n]...)
				t.outMu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				pty.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		t.done <- xpty.WaitProcess(ctx, cmd)
	}()

	return t, nil
}

// OutputLen returns the current length of the raw output buffer.
// Use before sending a prompt to mark the position for OutputSince.
func (t *Terminal) OutputLen() int {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	return len(t.outBuf)
}

// OutputSince returns raw PTY output accumulated after byte offset `from`.
// The returned slice is a copy — safe to hold across calls.
func (t *Terminal) OutputSince(from int) []byte {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	if from >= len(t.outBuf) {
		return nil
	}
	out := make([]byte, len(t.outBuf)-from)
	copy(out, t.outBuf[from:])
	return out
}

// Ignore makes io.Copy-style helpers discard errors (used for pump goroutines).
func ignore(_ int, _ error) {}

var _ io.Closer = (*Terminal)(nil)
