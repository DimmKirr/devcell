package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStopHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "stop", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("stop --help exited non-zero: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "Stop") {
		t.Errorf("expected 'Stop' in stop --help output, got:\n%s", out)
	}
}
