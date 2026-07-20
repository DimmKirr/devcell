package serve

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DimmKirr/devcell/internal/logger"
	"github.com/DimmKirr/devcell/internal/ptyctl"
)

// PTYExecutor drives Claude Code interactively via a virtual terminal.
// Unlike ShellExecutor (which spawns `claude -p` per request), PTYExecutor
// maintains a persistent Claude session and types prompts into the TUI.
type PTYExecutor struct {
	mu   sync.Mutex
	term *ptyctl.Terminal

	bin  string
	args []string

	readyMarker     string
	responseTimeout time.Duration
	stableDelay     time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

// PTYExecOption configures a PTYExecutor.
type PTYExecOption func(*PTYExecutor)

func WithPTYArgs(args ...string) PTYExecOption {
	return func(e *PTYExecutor) { e.args = args }
}

func WithReadyMarker(m string) PTYExecOption {
	return func(e *PTYExecutor) { e.readyMarker = m }
}

func WithResponseTimeout(d time.Duration) PTYExecOption {
	return func(e *PTYExecutor) { e.responseTimeout = d }
}

func WithStableDelay(d time.Duration) PTYExecOption {
	return func(e *PTYExecutor) { e.stableDelay = d }
}

// NewPTYExecutor creates a PTY-based executor. The bin argument is the path
// to the agent binary (normally "claude"). Default args include
// --dangerously-skip-permissions; override with WithPTYArgs for testing.
func NewPTYExecutor(bin string, opts ...PTYExecOption) *PTYExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	e := &PTYExecutor{
		bin:             bin,
		args:            []string{"--dangerously-skip-permissions"},
		readyMarker:     "❯",
		responseTimeout: 5 * time.Minute,
		stableDelay:     2 * time.Second,
		ctx:             ctx,
		cancel:          cancel,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *PTYExecutor) ensureStarted() error {
	if e.term != nil {
		select {
		case <-e.term.Done():
			e.term.Close()
			e.term = nil
		default:
			return nil
		}
	}

	term, err := ptyctl.StartWithOptions(e.ctx,
		[]ptyctl.StartOption{ptyctl.WithSize(120, 500)},
		e.bin, e.args...,
	)
	if err != nil {
		return fmt.Errorf("start pty: %w", err)
	}

	readyCtx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	if _, err := term.WaitFor(readyCtx, e.readyMarker); err != nil {
		term.Close()
		return fmt.Errorf("agent not ready: %w", err)
	}

	e.term = term
	return nil
}

// Run implements the Executor interface. It sends the prompt to the
// persistent PTY session and waits for the screen to stabilize before
// extracting the response via screen diff.
func (e *PTYExecutor) Run(opts ExecOpts) ExecResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	if opts.Agent != "claude" {
		return ExecResult{
			Stderr:   "pty executor only supports claude",
			ExitCode: 1,
		}
	}

	ctx, cancel := context.WithTimeout(e.ctx, e.responseTimeout)
	defer cancel()

	if err := e.ensureStarted(); err != nil {
		return ExecResult{
			Stderr:   err.Error(),
			ExitCode: 1,
		}
	}

	beforeScreen := e.term.Screen()

	if err := e.term.SendLine(opts.Prompt); err != nil {
		return ExecResult{
			Stderr:   fmt.Sprintf("send prompt: %v", err),
			ExitCode: 1,
		}
	}

	response, err := e.waitForStableResponse(ctx, beforeScreen)
	if err != nil {
		logger.Warn("pty response extraction failed", "err", err.Error())
		return ExecResult{
			Stdout:   response,
			Stderr:   err.Error(),
			ExitCode: 1,
		}
	}

	return ExecResult{Stdout: response}
}

// waitForStableResponse polls the terminal screen until it has changed from
// the before-snapshot and then remained unchanged for stableDelay.
func (e *PTYExecutor) waitForStableResponse(ctx context.Context, before string) (string, error) {
	var lastScreen string
	var lastChange time.Time

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return e.extractResponse(e.term.Screen(), before),
				fmt.Errorf("timed out waiting for response")
		case <-ticker.C:
			screen := e.term.Screen()
			if screen != lastScreen {
				lastScreen = screen
				lastChange = time.Now()
				continue
			}
			if screen == before {
				continue
			}
			if !lastChange.IsZero() && time.Since(lastChange) >= e.stableDelay {
				return e.extractResponse(screen, before), nil
			}
		}
	}
}

// extractResponse computes the new text that appeared on screen since the
// before-snapshot. The 500-row terminal buffer ensures large responses
// don't scroll off.
func (e *PTYExecutor) extractResponse(screen, before string) string {
	lines := strings.Split(screen, "\n")
	beforeLines := strings.Split(before, "\n")

	start := 0
	for i := 0; i < len(lines) && i < len(beforeLines); i++ {
		if lines[i] != beforeLines[i] {
			start = i
			break
		}
		if i == len(beforeLines)-1 {
			start = i + 1
		}
	}

	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	if end <= start {
		return strings.TrimSpace(screen)
	}

	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

// Close terminates the persistent PTY session.
func (e *PTYExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancel()
	if e.term != nil {
		err := e.term.Close()
		e.term = nil
		return err
	}
	return nil
}

var _ Executor = (*PTYExecutor)(nil)
