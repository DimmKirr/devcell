package op

import (
	"os/exec"
	"strings"
	"testing"
)

func TestResolveItems_StderrInError(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	// Stub: a script that prints a diagnostic to stderr and exits 1.
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", `echo "item not found" >&2; exit 1`)
	}

	_, errs := ResolveItems([]string{"no-such-item"})
	if len(errs) == 0 {
		t.Fatal("expected an error, got none")
	}
	msg := errs[0].Error()
	if !strings.Contains(msg, "item not found") {
		t.Errorf("error should contain stderr content, got: %s", msg)
	}
	if !strings.Contains(msg, "exit 1") {
		t.Errorf("error should contain exit code, got: %s", msg)
	}
}

func TestResolveItems_Success(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", `echo '{"fields":[{"label":"API_KEY","value":"secret123"},{"label":"DB_PASS","value":"pw"}]}'`)
	}

	env, errs := ResolveItems([]string{"test-doc"})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if env["API_KEY"] != "secret123" {
		t.Errorf("API_KEY = %q, want %q", env["API_KEY"], "secret123")
	}
	if env["DB_PASS"] != "pw" {
		t.Errorf("DB_PASS = %q, want %q", env["DB_PASS"], "pw")
	}
}

func TestResolveItems_EmptyStderrFallback(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	// Exit 1 with no stderr — should fall back to wrapping the raw error.
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", `exit 1`)
	}

	_, errs := ResolveItems([]string{"gone"})
	if len(errs) == 0 {
		t.Fatal("expected an error")
	}
	msg := errs[0].Error()
	if !strings.Contains(msg, "exit status 1") {
		t.Errorf("fallback error should contain exit status, got: %s", msg)
	}
}

func TestResolveItems_SkipsEmptyFields(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", `echo '{"fields":[{"label":"","value":"no-label"},{"label":"no-value","value":""},{"label":"OK","value":"yes"}]}'`)
	}

	env, errs := ResolveItems([]string{"partial"})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(env) != 1 || env["OK"] != "yes" {
		t.Errorf("expected only OK=yes, got: %v", env)
	}
}
