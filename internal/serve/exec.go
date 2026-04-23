package serve

import (
	"bytes"
	"os/exec"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/logger"
)

// ShellExecutor runs agent binaries as subprocesses.
type ShellExecutor struct{}

// Run executes the agent binary with the given prompt and optional model.
func (e *ShellExecutor) Run(agent, prompt, model string) ExecResult {
	var args []string
	switch agent {
	case "claude":
		args = append(args, "-p", prompt)
		if model != "" {
			args = append(args, "--model", model)
		}
	case "opencode":
		// opencode doesn't have a one-shot prompt mode yet;
		// pass prompt as positional arg for now.
		args = append(args, prompt)
		if model != "" {
			args = append(args, "--model", model)
		}
	}

	logger.Debug("exec agent", "agent", agent, "model", model)

	cmd := exec.Command(agent, args...)
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
		logger.Warn("agent failed", "agent", agent, "exit_code", exitCode, "duration", duration.String())
	} else {
		logger.Info("agent completed", "agent", agent, "duration", duration.String())
	}

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}
