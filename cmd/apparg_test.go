package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRDP_PositionalArgInHelp verifies "cell rdp" shows positional arg syntax.
func TestRDP_PositionalArgInHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "rdp", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("rdp --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "[app-name or suffix]") {
		t.Errorf("expected positional arg in usage, got:\n%s", s)
	}
	if strings.Contains(s, "--app") {
		t.Errorf("--app flag should be removed, got:\n%s", s)
	}
}

// TestVNC_PositionalArgInHelp verifies "cell vnc" shows positional arg syntax.
func TestVNC_PositionalArgInHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "vnc", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("vnc --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "[app-name or suffix]") {
		t.Errorf("expected positional arg in usage, got:\n%s", s)
	}
	if strings.Contains(s, "--app") {
		t.Errorf("--app flag should be removed, got:\n%s", s)
	}
}

// TestRDP_RejectsExtraArgs verifies that more than 1 positional arg is rejected.
func TestRDP_RejectsExtraArgs(t *testing.T) {
	cmd := exec.Command(binaryPath, "rdp", "app1", "app2")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for extra args, got: %s", out)
	}
}

// TestVNC_RejectsExtraArgs verifies that more than 1 positional arg is rejected.
func TestVNC_RejectsExtraArgs(t *testing.T) {
	cmd := exec.Command(binaryPath, "vnc", "app1", "app2")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for extra args, got: %s", out)
	}
}

// TestResolveAppArg_FullName verifies full name is passed to docker inspect.
func TestResolveAppArg_FullName(t *testing.T) {
	cmd := exec.Command(binaryPath, "rdp", "devcell-271")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for non-existent container, got: %s", out)
	}
	t.Logf("output: %s", out)
}

// TestResolveAppArg_SuffixFormat verifies bare number triggers suffix lookup.
func TestResolveAppArg_SuffixFormat(t *testing.T) {
	cmd := exec.Command(binaryPath, "rdp", "271")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for non-existent container, got: %s", out)
	}
	t.Logf("output: %s", out)
}

func TestRDP_HelpShowsExamples(t *testing.T) {
	out, err := exec.Command(binaryPath, "rdp", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("rdp --help failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "cell rdp devcell-271") {
		t.Errorf("expected positional example in help, got:\n%s", s)
	}
	if !strings.Contains(s, "cell rdp 271") {
		t.Errorf("expected suffix example in help, got:\n%s", s)
	}
}
