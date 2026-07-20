package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPTYExecutor_ImplementsExecutor(t *testing.T) {
	var _ Executor = (*PTYExecutor)(nil)
}

func TestPTYExecutor_RunExtractsResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("pty test requires PTY allocation")
	}

	script := writeFakeAgent(t)

	e := NewPTYExecutor(script,
		WithPTYArgs(),
		WithReadyMarker("READY>"),
		WithStableDelay(500*time.Millisecond),
		WithResponseTimeout(10*time.Second),
	)
	defer e.Close()

	result := e.Run(ExecOpts{Agent: "claude", Prompt: "hello"})

	if result.ExitCode != 0 {
		t.Fatalf("exit code %d; stderr: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "ECHO:hello") {
		t.Errorf("expected response to contain 'ECHO:hello'; got:\n%q", result.Stdout)
	}
}

func TestPTYExecutor_MultipleRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("pty test requires PTY allocation")
	}

	script := writeFakeAgent(t)

	e := NewPTYExecutor(script,
		WithPTYArgs(),
		WithReadyMarker("READY>"),
		WithStableDelay(500*time.Millisecond),
		WithResponseTimeout(10*time.Second),
	)
	defer e.Close()

	r1 := e.Run(ExecOpts{Agent: "claude", Prompt: "first"})
	if r1.ExitCode != 0 {
		t.Fatalf("run 1: exit %d: %s", r1.ExitCode, r1.Stderr)
	}
	if !strings.Contains(r1.Stdout, "ECHO:first") {
		t.Errorf("run 1: want 'ECHO:first'; got:\n%q", r1.Stdout)
	}

	r2 := e.Run(ExecOpts{Agent: "claude", Prompt: "second"})
	if r2.ExitCode != 0 {
		t.Fatalf("run 2: exit %d: %s", r2.ExitCode, r2.Stderr)
	}
	if !strings.Contains(r2.Stdout, "ECHO:second") {
		t.Errorf("run 2: want 'ECHO:second'; got:\n%q", r2.Stdout)
	}
}

func TestPTYExecutor_RejectsNonClaude(t *testing.T) {
	e := NewPTYExecutor("/bin/true")
	defer e.Close()

	result := e.Run(ExecOpts{Agent: "opencode", Prompt: "hi"})
	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit for non-claude agent")
	}
}

func TestPTYExecutor_RestartAfterCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("pty test requires PTY allocation")
	}

	script := writeFakeAgentCrashable(t)

	e := NewPTYExecutor(script,
		WithPTYArgs(),
		WithReadyMarker("READY>"),
		WithStableDelay(500*time.Millisecond),
		WithResponseTimeout(10*time.Second),
	)
	defer e.Close()

	// First run succeeds
	r1 := e.Run(ExecOpts{Agent: "claude", Prompt: "before-crash"})
	if r1.ExitCode != 0 {
		t.Fatalf("run 1: exit %d: %s", r1.ExitCode, r1.Stderr)
	}
	if !strings.Contains(r1.Stdout, "ECHO:before-crash") {
		t.Errorf("run 1: want 'ECHO:before-crash'; got:\n%q", r1.Stdout)
	}

	// Send magic word that makes the fake agent exit
	r2 := e.Run(ExecOpts{Agent: "claude", Prompt: "CRASH"})
	// This run may fail (process died mid-response) — that's expected
	_ = r2

	// Give the process time to exit
	time.Sleep(300 * time.Millisecond)

	// Third run should auto-restart and succeed
	r3 := e.Run(ExecOpts{Agent: "claude", Prompt: "after-crash"})
	if r3.ExitCode != 0 {
		t.Fatalf("run 3 (after restart): exit %d: %s", r3.ExitCode, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "ECHO:after-crash") {
		t.Errorf("run 3: want 'ECHO:after-crash'; got:\n%q", r3.Stdout)
	}
}

func TestPTYExecutor_CloseIdempotent(t *testing.T) {
	e := NewPTYExecutor("/bin/true")
	if err := e.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestPTYExecutor_MultilineResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("pty test requires PTY allocation")
	}

	script := writeFakeAgentMultiline(t)

	e := NewPTYExecutor(script,
		WithPTYArgs(),
		WithReadyMarker("READY>"),
		WithStableDelay(500*time.Millisecond),
		WithResponseTimeout(10*time.Second),
	)
	defer e.Close()

	result := e.Run(ExecOpts{Agent: "claude", Prompt: "hello"})
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d; stderr: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "LINE1:hello") {
		t.Errorf("missing LINE1; got:\n%q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "LINE2:hello") {
		t.Errorf("missing LINE2; got:\n%q", result.Stdout)
	}
}

func TestPTYExecutor_Integration_RealClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("long: requires authenticated claude binary")
	}

	claudeBin := "/opt/devcell/.local/state/nix/profiles/profile/bin/claude"
	if _, err := os.Stat(claudeBin); err != nil {
		t.Skip("claude not available:", err)
	}

	e := NewPTYExecutor(claudeBin,
		WithResponseTimeout(2*time.Minute),
		WithStableDelay(3*time.Second),
	)
	defer e.Close()

	result := e.Run(ExecOpts{
		Agent:  "claude",
		Prompt: "respond with exactly the word PONG and nothing else",
	})

	t.Logf("ExitCode: %d", result.ExitCode)
	t.Logf("Stdout (%d bytes):\n%s", len(result.Stdout), result.Stdout)
	if result.Stderr != "" {
		t.Logf("Stderr:\n%s", result.Stderr)
	}

	if result.ExitCode != 0 {
		t.Fatalf("exit code %d; stderr: %s", result.ExitCode, result.Stderr)
	}

	if !strings.Contains(strings.ToUpper(result.Stdout), "PONG") {
		t.Errorf("expected PONG in response; got:\n%s", result.Stdout)
	}
}

func writeFakeAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	content := `#!/bin/sh
printf "Welcome to fake claude\nREADY> "
while IFS= read -r line; do
    printf "ECHO:%s\n" "$line"
    printf "READY> "
done
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeAgentCrashable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	content := `#!/bin/sh
printf "Welcome to fake claude\nREADY> "
while IFS= read -r line; do
    if [ "$line" = "CRASH" ]; then
        exit 1
    fi
    printf "ECHO:%s\n" "$line"
    printf "READY> "
done
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeAgentMultiline(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	content := `#!/bin/sh
printf "Welcome to fake claude\nREADY> "
while IFS= read -r line; do
    printf "LINE1:%s\n" "$line"
    printf "LINE2:%s\n" "$line"
    printf "LINE3:%s\n" "$line"
    printf "READY> "
done
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
