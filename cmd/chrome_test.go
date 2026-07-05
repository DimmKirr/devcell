package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAuthChrome_HelpShowsAppNameArg verifies "cell auth chrome" shows the app-name positional arg.
func TestAuthChrome_HelpShowsAppNameArg(t *testing.T) {
	out, err := exec.Command(binaryPath, "auth", "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("auth chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "[app-name]") {
		t.Errorf("expected [app-name] in usage, got:\n%s", s)
	}
}

// TestAuthChrome_HelpShowsExamples verifies help includes key examples.
func TestAuthChrome_HelpShowsExamples(t *testing.T) {
	out, err := exec.Command(binaryPath, "auth", "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("auth chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	for _, want := range []string{
		"cell auth chrome tripit",
		"--sync",
		"--no-sync",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in help, got:\n%s", want, s)
		}
	}
}

// TestAuthChrome_SyncRequiresBrowser verifies --sync errors without a running browser.
func TestAuthChrome_SyncRequiresBrowser(t *testing.T) {
	cmd := exec.Command(binaryPath, "auth", "chrome", "--sync", "test-app")
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

// TestAuthChrome_FlagsRegistered verifies --sync and --no-sync flags exist.
func TestAuthChrome_FlagsRegistered(t *testing.T) {
	out, err := exec.Command(binaryPath, "auth", "chrome", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("auth chrome --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "--sync") {
		t.Errorf("expected --sync flag in help")
	}
	if !strings.Contains(s, "--no-sync") {
		t.Errorf("expected --no-sync flag in help")
	}
}

// TestAuthChrome_ListedUnderAuth verifies "cell auth --help" lists the chrome subcommand.
func TestAuthChrome_ListedUnderAuth(t *testing.T) {
	out, err := exec.Command(binaryPath, "auth", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("auth --help failed: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "chrome") {
		t.Errorf("expected 'chrome' subcommand in auth --help, got:\n%s", s)
	}
	if !strings.Contains(s, "kube") {
		t.Errorf("expected 'kube' subcommand in auth --help, got:\n%s", s)
	}
}

// TestRootChrome_Removed verifies "cell chrome" no longer exists as a root subcommand.
func TestRootChrome_Removed(t *testing.T) {
	out, _ := exec.Command(binaryPath, "--help").CombinedOutput()
	s := string(out)
	// Root help lists subcommands in "Available Commands:" — chrome should NOT appear there.
	if strings.Contains(s, "\n  chrome ") || strings.Contains(s, "\n  chrome\t") {
		t.Errorf("expected `chrome` removed from root command list, got:\n%s", s)
	}
}

// TestRootLogin_Removed verifies "cell login" no longer exists.
func TestRootLogin_Removed(t *testing.T) {
	out, _ := exec.Command(binaryPath, "--help").CombinedOutput()
	s := string(out)
	if strings.Contains(s, "\n  login ") || strings.Contains(s, "\n  login\t") {
		t.Errorf("expected `login` removed from root command list, got:\n%s", s)
	}
}
