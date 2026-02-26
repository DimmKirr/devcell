package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// binaryPath returns the path where the test binary is built.
// Tests build it once via TestMain.
var binaryPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "cell-smoke-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	binaryPath = tmp + "/cell"
	out, err := exec.Command("go", "build", "-o", binaryPath, ".").CombinedOutput()
	if err != nil {
		panic("go build failed: " + string(out))
	}

	os.Exit(m.Run())
}

func TestHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "Usage:") {
		t.Errorf("expected 'Usage:' in --help output, got:\n%s", out)
	}
}

func TestVersion(t *testing.T) {
	out, err := exec.Command(binaryPath, "--version").Output()
	if err != nil {
		t.Fatalf("--version exited non-zero: %v", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Error("--version produced empty output")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	cmd := exec.Command(binaryPath, "definitely-not-a-command")
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for unknown subcommand")
	}
}

func TestDebugFlagInHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "--debug") {
		t.Errorf("--debug flag not found in --help output:\n%s", out)
	}
}

func TestPlainTextFlagInHelp(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "--plain-text") {
		t.Errorf("--plain-text flag not found in --help output:\n%s", out)
	}
}

// scaffoldedHome creates a temp HOME with a minimal devcell.toml so the CLI
// skips the first-run interactive prompt.
func scaffoldedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cfgDir := home + "/.config/devcell"
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgDir+"/devcell.toml", []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestPlainTextNoSpinnerChars verifies that --plain-text suppresses pterm spinner
// Unicode sequences. We run with --dry-run to avoid docker exec but still
// exercise the pre-exec ux output path.
func TestPlainTextNoSpinnerChars(t *testing.T) {
	spinnerChars := []string{"⡀", "⢀", "⠄", "⠠", "⠐", "⠂", "⠁", "⠈"}

	cmd := exec.Command(binaryPath, "--plain-text", "shell", "--dry-run")
	cmd.Env = append(os.Environ(),
		"CELL_ID=1",
		"HOME="+scaffoldedHome(t),
	)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	for _, ch := range spinnerChars {
		if strings.Contains(s, ch) {
			t.Errorf("spinner char %q found in --plain-text output:\n%s", ch, s)
		}
	}
}

// TestDebugNoSpinnerChars verifies --debug also suppresses spinners.
func TestDebugNoSpinnerChars(t *testing.T) {
	spinnerChars := []string{"⡀", "⢀", "⠄", "⠠", "⠐", "⠂", "⠁", "⠈"}

	cmd := exec.Command(binaryPath, "--debug", "shell", "--dry-run")
	cmd.Env = append(os.Environ(),
		"CELL_ID=1",
		"HOME="+scaffoldedHome(t),
	)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	for _, ch := range spinnerChars {
		if strings.Contains(s, ch) {
			t.Errorf("spinner char %q found in --debug output:\n%s", ch, s)
		}
	}
}
