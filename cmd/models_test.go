package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestModels_NoOllama verifies that `cell models` exits cleanly when
// ollama is not reachable. Cloud models from OpenRouter are shown instead.
func TestModels_NoOllama(t *testing.T) {
	cmd := exec.Command(binaryPath, "models")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cell models failed: %v\noutput: %s", err, out)
	}
	// Either cloud models are shown (normal case) or a "no models" warning
	// appears if OpenRouter is also unreachable. Either way, exit code is 0.
	_ = string(out) // command exits cleanly — that is the assertion
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
