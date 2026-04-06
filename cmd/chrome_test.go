package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestChrome_HelpShowsAppNameArg verifies "cell chrome" shows the app-name positional arg.
func TestChrome_HelpShowsAppNameArg(t *testing.T) {
	out, err := exec.Command(binaryPath, "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "[app-name]") {
		t.Errorf("expected [app-name] in usage, got:\n%s", s)
	}
}

// TestChrome_HelpShowsExamples verifies help includes key examples.
func TestChrome_HelpShowsExamples(t *testing.T) {
	out, err := exec.Command(binaryPath, "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	for _, want := range []string{
		"cell chrome tripit",
		"--sync",
		"--no-sync",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in help, got:\n%s", want, s)
		}
	}
}

// TestChrome_SyncRequiresBrowser verifies --sync errors without a running browser.
func TestChrome_SyncRequiresBrowser(t *testing.T) {
	cmd := exec.Command(binaryPath, "chrome", "--sync", "test-app")
	cmd.Env = append(cmd.Environ(),
		"HOME="+t.TempDir(),
		"TMUX_PANE=%0",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error, got success: %s", out)
	}
	s := string(out)
	if !strings.Contains(s, "requires a running browser") {
		t.Errorf("expected 'requires a running browser' error, got:\n%s", s)
	}
}

// TestChrome_FlagsRegistered verifies --sync and --no-sync flags exist.
func TestChrome_FlagsRegistered(t *testing.T) {
	out, err := exec.Command(binaryPath, "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "--sync") {
		t.Errorf("expected --sync flag in help")
	}
	if !strings.Contains(s, "--no-sync") {
		t.Errorf("expected --no-sync flag in help")
	}
}

// TestLogin_HelpShowsURL verifies "cell login" shows URL arg.
func TestLogin_HelpShowsURL(t *testing.T) {
	out, err := exec.Command(binaryPath, "login", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("login --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "<url>") {
		t.Errorf("expected <url> in usage, got:\n%s", s)
	}
	if !strings.Contains(s, "cell login https://tripit.com") {
		t.Errorf("expected example in help, got:\n%s", s)
	}
}

// TestLogin_RequiresURL verifies "cell login" without args errors.
func TestLogin_RequiresURL(t *testing.T) {
	cmd := exec.Command(binaryPath, "login")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for missing URL, got: %s", out)
	}
}
