package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tartTestHome sets up a temp HOME with config dir and .devcell.toml for tart tests.
func tartTestHome(t *testing.T) string {
	t.Helper()
	return tartTestHomeWithTOML(t, "[cell]\n")
}

// tartTestHomeWithTOML sets up a temp HOME with custom project TOML content.
func tartTestHomeWithTOML(t *testing.T, projectTOML string) string {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "devcell")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".devcell.toml"), []byte(projectTOML), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestEngineTart_DryRunPrintsTartExec checks that --engine=tart --dry-run
// prints a tart exec argv and exits 0.
func TestEngineTart_DryRunPrintsTartExec(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "tart exec") {
		t.Errorf("expected 'tart exec' in dry-run output, got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("tart engine should not print docker run argv, got:\n%s", s)
	}
	if strings.Contains(strings.ToLower(s), "not yet implemented") {
		t.Errorf("dry-run should not print 'not yet implemented', got:\n%s", s)
	}
}

// TestEngineTart_DryRunContainsBinary checks the agent binary name appears in output.
func TestEngineTart_DryRunContainsBinary(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "claude") {
		t.Errorf("expected 'claude' in dry-run output, got:\n%s", out)
	}
}

// TestEngineTart_DryRunNoDocker checks that no docker commands appear in tart dry-run.
func TestEngineTart_DryRunNoDocker(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if strings.Contains(string(out), "docker") {
		t.Errorf("tart engine should not involve docker, got:\n%s", out)
	}
}

// TestBackgroundFlag_StrippedFromArgsTart checks that --background is stripped
// from forwarded args (not passed to the inner binary).
func TestBackgroundFlag_StrippedFromArgsTart(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "--background", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "--background") {
		t.Errorf("--background should be stripped from forwarded args, got:\n%s", s)
	}
}

// TestEngineTart_EngineHelpIncludesTart checks --engine help text mentions tart.
func TestEngineTart_EngineHelpIncludesTart(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "tart") {
		t.Errorf("expected 'tart' in --help output for --engine flag, got:\n%s", out)
	}
}

// TestEngineTart_NoDebugOnLinux checks that tart without --debug errors on non-darwin.
func TestEngineTart_NoDebugOnLinux(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this test validates the non-darwin error path")
	}
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error on non-darwin without --debug, got exit 0:\n%s", out)
	}
	s := string(out)
	if !strings.Contains(s, "tart engine requires macOS") {
		t.Errorf("expected 'tart engine requires macOS' in error, got:\n%s", s)
	}
	if !strings.Contains(s, "--debug to simulate") {
		t.Errorf("expected '--debug to simulate' hint in error, got:\n%s", s)
	}
}

// TestEngineTart_DebugMockOutput checks that --debug prints mock/simulation output.
func TestEngineTart_DebugMockOutput(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "--debug", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 with --debug mock, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	for _, want := range []string{
		"[MOCK",
		"mock mode",
		"tart exec (no SSH)",
		"would exec",
		"[tart] tart run",
		"guest agent ready",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in debug mock output, got:\n%s", want, s)
		}
	}
}

// TestEngineTart_DebugMockNoDocker checks mock output has no docker references.
func TestEngineTart_DebugMockNoDocker(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "--debug", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if strings.Contains(string(out), "docker") {
		t.Errorf("mock output should not mention docker, got:\n%s", out)
	}
}

// TestEngineTart_DryRunContainsNixSource checks that the nix daemon source command
// appears in the dry-run output.
func TestEngineTart_DryRunContainsNixSource(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "nix-daemon.sh") {
		t.Errorf("expected nix-daemon.sh source in dry-run output, got:\n%s", out)
	}
}

// TestEngineTart_DryRunContainsEnvVars checks that env vars appear in dry-run output.
func TestEngineTart_DryRunContainsEnvVars(t *testing.T) {
	home := tartTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=tart", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home, "TERM=xterm-256color")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "TERM=") {
		t.Errorf("expected TERM= in dry-run output, got:\n%s", out)
	}
}
