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

// claudeArgs builds the claude argv shared by the buffered (Run, format=json)
// and streaming (streamClaude, format=stream-json) execution paths.
//
// --dangerously-skip-permissions matches cell claude's default
// (cmd/claude.go:40). Without it, any tool use (Read/Bash/Write/...)
// hits claude's permission gate; since stdin is a bytes.Buffer and
// not a TTY, the gate either fails or hangs the HTTP request. The
// operator's auth boundary on cell serve is DEVCELL_API_KEY, so
// the permission gate is not adding a meaningful second layer here.
//
// --output-format=json wraps the assistant text in a result envelope with
// per-turn usage (input/output/cache tokens) and total cost; --output-format
// =stream-json (with --include-partial-messages) emits the same `result`
// envelope as the terminal line, prefixed by a stream of Anthropic
// Messages-API `stream_event` wrappers carrying token-level deltas.
func claudeArgs(opts ExecOpts, format string) []string {
	args := []string{
		"--dangerously-skip-permissions",
		"--output-format", format,
	}
	if format == "stream-json" {
		args = append(args, "--include-partial-messages")
	}
	args = append(args, "-p", opts.Prompt)
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.BasePromptFile != "" {
		args = append(args, "--system-prompt-file", opts.BasePromptFile)
	}
	if opts.SystemPromptFile != "" {
		args = append(args, "--append-system-prompt-file", opts.SystemPromptFile)
	}
	return args
}

// Run executes the agent binary with the given options.
func (e *ShellExecutor) Run(opts ExecOpts) ExecResult {
	var args []string
	switch opts.Agent {
	case "claude":
		args = claudeArgs(opts, "json")
	case "opencode":
		// opencode doesn't have a one-shot prompt mode yet;
		// pass prompt as positional arg for now.
		args = append(args, opts.Prompt)
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		}
		// opencode has no --effort or --append-system-prompt equivalent; ignore.
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
	res := ExecResult{
		Stdout:   strings.TrimRight(stdout.String(), "\n"),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}

	// Decode claude's --output-format=json envelope into Stdout (the text)
	// + Usage (token + cost telemetry). On parse failure (claude crashed
	// mid-output, version skew, etc.) we keep the raw bytes as Stdout —
	// degraded but functional, with a Warn line so the operator notices.
	if opts.Agent == "claude" && exitCode == 0 && len(res.Stdout) > 0 {
		text, usage, perr := parseClaudeJSON([]byte(res.Stdout))
		if perr != nil {
			logger.Warn("claude json parse failed; falling back to raw stdout",
				"err", perr.Error(), "raw_first_200", truncate(res.Stdout, 200))
		} else {
			res.Stdout = text
			res.Usage = usage
		}
	}
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
