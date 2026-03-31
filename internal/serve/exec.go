package serve

import (
	"bytes"
	"os/exec"
	"syscall"
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

	cmd := exec.Command(agent, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
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

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}
