package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeStubAgent writes a shell script at <dir>/<name> that records its argv to
// <dir>/<name>.args and prints a fixed string. Returns the dir to put on PATH.
func makeStubAgent(t *testing.T, name, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo \"$@\" > \"" + filepath.Join(dir, name+".args") + "\"\n" +
		"printf '%s' '" + stdout + "'\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return dir
}

func readArgs(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".args"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", old) })
	os.Setenv("PATH", dir)
}

func TestShellExecutor_ClaudePermissionsBypassDefault(t *testing.T) {
	// cell serve must mirror cell claude's --dangerously-skip-permissions
	// default — otherwise tool use hangs the HTTP request on the
	// permission gate (no TTY for stdin).
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	args := readArgs(t, dir, "claude")
	if !strings.Contains(args, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions in argv, got %q", args)
	}
}

func TestShellExecutor_OpenCodeNoPermissionsFlag(t *testing.T) {
	// opencode has no equivalent flag; ensure we don't accidentally pass
	// --dangerously-skip-permissions to it.
	dir := makeStubAgent(t, "opencode", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "opencode")
	if strings.Contains(args, "--dangerously-skip-permissions") {
		t.Errorf("opencode should not receive --dangerously-skip-permissions, got argv %q", args)
	}
}

func TestShellExecutor_ClaudeAppendsEffortFlag(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{
		Agent:  "claude",
		Prompt: "hi",
		Model:  "sonnet",
		Effort: "high",
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "ok" {
		t.Errorf("stdout = %q, want ok", res.Stdout)
	}

	args := readArgs(t, dir, "claude")
	if !strings.Contains(args, "--effort high") {
		t.Errorf("expected --effort high in argv, got %q", args)
	}
	if !strings.Contains(args, "--model sonnet") {
		t.Errorf("expected --model sonnet in argv, got %q", args)
	}
	// -p hi must come before flags, but we don't pin exact order — just presence.
	if !strings.Contains(args, "-p hi") {
		t.Errorf("expected -p hi in argv, got %q", args)
	}
}

func TestShellExecutor_ClaudeNoEffortNoFlag(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi", Model: "sonnet"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "claude")
	if strings.Contains(args, "--effort") {
		t.Errorf("expected no --effort flag when Effort empty, got %q", args)
	}
}

func TestShellExecutor_OpenCodeIgnoresEffort(t *testing.T) {
	// opencode has no --effort flag; ExecOpts.Effort should not produce one.
	dir := makeStubAgent(t, "opencode", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi", Effort: "high"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "opencode")
	if strings.Contains(args, "--effort") {
		t.Errorf("opencode should not receive --effort, got argv %q", args)
	}
}

func TestShellExecutor_ClaudeAppendsSystemPromptFileFlag(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{
		Agent:            "claude",
		Prompt:           "hi",
		SystemPromptFile: "/devcell-85/.devcell/prompts/main/additional-systemprompt.md",
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	args := readArgs(t, dir, "claude")
	if !strings.Contains(args, "--append-system-prompt-file /devcell-85/.devcell/prompts/main/additional-systemprompt.md") {
		t.Errorf("expected --append-system-prompt-file flag in argv, got %q", args)
	}
	// The inline form is mutually exclusive with the file form — claude
	// rejects both together, so it must not appear.
	if strings.Contains(args, "--append-system-prompt ") {
		t.Errorf("inline --append-system-prompt must not be emitted alongside the file form, got %q", args)
	}
}

func TestShellExecutor_ClaudeNoSystemPromptNoFlag(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "claude")
	if strings.Contains(args, "--append-system-prompt") {
		t.Errorf("expected no --append-system-prompt-file flag when SystemPromptFile empty, got %q", args)
	}
}

func TestShellExecutor_OpenCodeIgnoresSystemPrompt(t *testing.T) {
	dir := makeStubAgent(t, "opencode", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi", SystemPromptFile: "/tmp/x.md"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "opencode")
	if strings.Contains(args, "--append-system-prompt") {
		t.Errorf("opencode should not receive --append-system-prompt, got argv %q", args)
	}
}

func TestShellExecutor_ClaudeUsesJSONOutputFormat(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	_ = e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})

	args := readArgs(t, dir, "claude")
	if !strings.Contains(args, "--output-format json") {
		t.Errorf("expected --output-format json in claude argv, got %q", args)
	}
}

func TestShellExecutor_ClaudeJSONResultExtractedToStdout(t *testing.T) {
	// Stub claude that emits the real JSON envelope. Stdout must end up
	// as the unwrapped "result" string, and Usage must reflect the token
	// counts from the JSON.
	dir := makeStubAgent(t, "claude", realClaudeJSON)
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})

	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "Hi." {
		t.Errorf("Stdout = %q, want %q (should be unwrapped from JSON envelope)", res.Stdout, "Hi.")
	}
	if res.Usage.InputTokens != 5 || res.Usage.OutputTokens != 8 {
		t.Errorf("usage not parsed: %+v", res.Usage)
	}
	if res.Usage.CacheCreationInputTokens != 58225 {
		t.Errorf("CacheCreationInputTokens = %d, want 58225", res.Usage.CacheCreationInputTokens)
	}
}

func TestShellExecutor_ClaudeJSONParseFailureFallsBackToRaw(t *testing.T) {
	// Garbled output (claude crashed mid-write, version skew, etc.). We
	// must not lose the bytes — keep them as Stdout, leave Usage zero.
	dir := makeStubAgent(t, "claude", "not actually json output")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})

	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if res.Stdout != "not actually json output" {
		t.Errorf("Stdout = %q, want raw fallback", res.Stdout)
	}
	if (res.Usage != Usage{}) {
		t.Errorf("Usage should be zero on parse failure, got %+v", res.Usage)
	}
}

func TestShellExecutor_OpenCodeNoJSONFlag(t *testing.T) {
	// opencode doesn't emit JSON — it must NOT receive --output-format,
	// and Usage stays zero.
	dir := makeStubAgent(t, "opencode", "plain text reply")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi"})

	args := readArgs(t, dir, "opencode")
	if strings.Contains(args, "--output-format") {
		t.Errorf("opencode should not receive --output-format, got argv %q", args)
	}
	if res.Stdout != "plain text reply" {
		t.Errorf("Stdout = %q", res.Stdout)
	}
	if (res.Usage != Usage{}) {
		t.Errorf("Usage should be zero for opencode, got %+v", res.Usage)
	}
}

func TestShellExecutor_NonZeroExitPropagated(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho oops 1>&2\nexit 7\n"
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})
	if res.ExitCode != 7 {
		t.Errorf("exit = %d, want 7", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "oops") {
		t.Errorf("stderr = %q, want contains oops", res.Stderr)
	}
}

// A configured base replaces Claude Code's built-in prompt; it travels as a
// file alongside the overlay, and both flags may appear together.
func TestShellExecutor_ClaudeEmitsBaseAndOverlayFlags(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{
		Agent:            "claude",
		Prompt:           "hi",
		BasePromptFile:   "/devcell-85/.devcell/prompts/main/system-prompt.md",
		SystemPromptFile: "/devcell-85/.devcell/prompts/main/additional-systemprompt.md",
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	args := readArgs(t, dir, "claude")
	if !strings.Contains(args, "--system-prompt-file /devcell-85/.devcell/prompts/main/system-prompt.md") {
		t.Errorf("expected --system-prompt-file in argv, got %q", args)
	}
	if !strings.Contains(args, "--append-system-prompt-file /devcell-85/.devcell/prompts/main/additional-systemprompt.md") {
		t.Errorf("expected --append-system-prompt-file in argv, got %q", args)
	}
}

func TestShellExecutor_ClaudeNoBasePromptNoFlag(t *testing.T) {
	dir := makeStubAgent(t, "claude", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "claude", Prompt: "hi"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "claude")
	if strings.Contains(args, "--system-prompt-file") {
		t.Errorf("base flag must not appear when unconfigured — stock prompt must survive, got %q", args)
	}
}

func TestShellExecutor_OpenCodeIgnoresBasePrompt(t *testing.T) {
	dir := makeStubAgent(t, "opencode", "ok")
	withPath(t, dir)

	e := &ShellExecutor{}
	res := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi", BasePromptFile: "/tmp/base.md"})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	args := readArgs(t, dir, "opencode")
	if strings.Contains(args, "--system-prompt-file") {
		t.Errorf("opencode should not receive --system-prompt-file, got %q", args)
	}
}
