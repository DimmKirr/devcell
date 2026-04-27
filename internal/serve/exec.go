package serve

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/logger"
)

// ShellExecutor runs agent binaries as subprocesses.
type ShellExecutor struct{}

// Run executes the agent binary with the given options.
func (e *ShellExecutor) Run(opts ExecOpts) ExecResult {
	var args []string
	switch opts.Agent {
	case "claude":
		args = append(args, "-p", opts.Prompt)
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		}
		if opts.Effort != "" {
			args = append(args, "--effort", opts.Effort)
		}
	case "opencode":
		// opencode doesn't have a one-shot prompt mode yet;
		// pass prompt as positional arg for now.
		args = append(args, opts.Prompt)
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		}
		// opencode has no --effort equivalent; ignore.
	}

	logger.Debug("exec agent", "agent", opts.Agent, "model", opts.Model, "effort", opts.Effort)

	cmd := exec.Command(opts.Agent, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			} else {
				exitCode = 1
			}
		} else {
			exitCode = 1
			stderr.WriteString(err.Error())
		}
	}

	if exitCode != 0 {
		logger.Warn("agent failed", "agent", opts.Agent, "exit_code", exitCode, "duration", duration.String())
	} else {
		logger.Info("agent completed", "agent", opts.Agent, "duration", duration.String())
	}

	// Agent CLIs (claude, opencode) terminate stdout with a trailing newline,
	// which would leak into output_text on /v1/responses and message.content
	// on /v1/chat/completions. Strip only trailing newlines — preserves any
	// intentional leading whitespace and indentation inside the answer.
	return ExecResult{
		Stdout:   strings.TrimRight(stdout.String(), "\n"),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}
