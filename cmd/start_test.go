package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStartHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "start", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("start --help exited non-zero: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "background") {
		t.Errorf("expected 'background' in start --help output, got:\n%s", out)
	}
}

func TestStart_DryRun(t *testing.T) {
	home := scaffoldedHome(t)
	cmd := exec.Command(binaryPath, "--dry-run", "--plain-text", "start")
	cmd.Dir = home
	cmd.Env = append(os.Environ(),
		"DEVCELL_BUNK=0",
		"HOME="+home,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start --dry-run failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, " -d ") {
		t.Errorf("expected -d in dry-run output:\n%s", s)
	}
	if !strings.Contains(s, "sleep infinity") {
		t.Errorf("expected 'sleep infinity' in dry-run output:\n%s", s)
	}
	if strings.Contains(s, " -it ") {
		t.Errorf("-it should not appear in detached mode:\n%s", s)
	}
}
