package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestClaude_SessionID_Injected verifies that CLAUDE_CODE_SESSION_ID is
// injected into the docker argv as a deterministic UUID derived from APP_NAME.
func TestClaude_SessionID_Injected(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "CLAUDE_CODE_SESSION_ID=") {
		t.Fatalf("expected CLAUDE_CODE_SESSION_ID in argv:\n%s", argv)
	}
}

// TestClaude_SessionID_Deterministic verifies that the same bunk + project
// always produces the same session ID.
func TestClaude_SessionID_Deterministic(t *testing.T) {
	home := scaffoldedHome(t)

	run := func() string {
		cmd := exec.Command(binaryPath, "claude", "--dry-run")
		cmd.Dir = home
		cmd.Env = append(os.Environ(), "DEVCELL_BUNK=42", "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
		}
		return extractEnvFromArgv(string(out), "CLAUDE_CODE_SESSION_ID")
	}

	id1 := run()
	id2 := run()
	if id1 == "" {
		t.Fatal("CLAUDE_CODE_SESSION_ID is empty")
	}
	if id1 != id2 {
		t.Errorf("session ID not deterministic: %q != %q", id1, id2)
	}
}

// TestClaude_SessionID_DiffersByBunk verifies that different bunks produce
// different session IDs for the same project.
func TestClaude_SessionID_DiffersByBunk(t *testing.T) {
	home := scaffoldedHome(t)

	runWithBunk := func(bunk string) string {
		cmd := exec.Command(binaryPath, "claude", "--dry-run")
		cmd.Dir = home
		cmd.Env = append(os.Environ(), "DEVCELL_BUNK="+bunk, "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
		}
		return extractEnvFromArgv(string(out), "CLAUDE_CODE_SESSION_ID")
	}

	id1 := runWithBunk("1")
	id2 := runWithBunk("2")
	if id1 == id2 {
		t.Errorf("same session ID for different bunks: %q", id1)
	}
}

// TestOpencode_NoSessionFlag verifies that opencode does NOT get --session
// injected. OpenCode's --session requires an existing session ID (no
// create-or-resume), so we don't pass it.
func TestOpencode_NoSessionFlag(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "--session") {
		t.Fatalf("--session should NOT be in opencode argv:\n%s", argv)
	}
}
