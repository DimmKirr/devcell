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
