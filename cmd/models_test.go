package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestModels_NoOllama verifies that `cell models` exits cleanly when
// ollama is not reachable (no error, informative message).
func TestModels_NoOllama(t *testing.T) {
	cmd := exec.Command(binaryPath, "models")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cell models failed: %v\noutput: %s", err, out)
	}

	s := string(out)
	if !strings.Contains(s, "not reachable") && !strings.Contains(s, "not detected") &&
		!strings.Contains(s, "No ollama") && !strings.Contains(s, "not found") {
		t.Errorf("expected 'not reachable/detected' message, got:\n%s", s)
	}
}

// TestModels_InHelp verifies the models command appears in --help output.
func TestModels_InHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "models") {
		t.Errorf("'models' command not found in --help output:\n%s", out)
	}
}
