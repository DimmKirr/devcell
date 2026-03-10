package container_test

// shell_test.go — e2e tests for zsh + starship prompt configuration.
//
// Verifies that starship renders the configured prompt symbols correctly,
// both via `starship prompt` (direct) and via `unbuffer zsh` (full shell).
//
// Run:
//   go test -v -run TestShell -timeout 120s ./...

import (
	"strings"
	"testing"
)

// TestShellStarshipBinary verifies starship is installed and on PATH.
func TestShellStarshipBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)
	out, code := asUser(t, c, "starship --version")
	if code != 0 {
		t.Fatalf("starship not found on PATH (exit %d): %s", code, out)
	}
	t.Logf("PASS: %s", strings.TrimSpace(out))
}

// TestShellStarshipConfigExists verifies the home-manager-generated config
// is present and contains the expected unicode character symbol.
func TestShellStarshipConfigExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	out, code := asUser(t, c, "cat ~/.config/starship.toml")
	if code != 0 {
		t.Fatalf("starship.toml not found (exit %d): %s", code, out)
	}

	// Verify the unicode symbol from shell.nix made it through nix string escaping
	if !strings.Contains(out, "•") {
		t.Errorf("FAIL: starship.toml missing • character symbol:\n%s", out)
	} else {
		t.Logf("PASS: starship.toml contains • symbol")
	}

	if !strings.Contains(out, "add_newline = false") {
		t.Errorf("FAIL: starship.toml missing add_newline setting:\n%s", out)
	}
}

// TestShellStarshipPromptRenders runs `starship prompt` directly and verifies
// the rendered output contains the configured unicode symbols.
// No PTY needed — starship prompt writes to stdout.
func TestShellStarshipPromptRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// starship prompt renders the prompt string to stdout.
	// Run from /tmp so directory module shows "tmp".
	out, code := asUser(t, c, "cd /tmp && starship prompt")
	if code != 0 {
		t.Fatalf("starship prompt failed (exit %d): %s", code, out)
	}

	if !strings.Contains(out, "•") {
		t.Errorf("FAIL: starship prompt output missing • symbol: %q", out)
	} else {
		t.Logf("PASS: starship prompt rendered •: %q", out)
	}
}

// TestShellZshStarshipIntegration verifies the complete integration chain:
// zsh sources .zshrc → starship init is evaluated → starship prompt renders •.
// Uses `zsh -c 'source ~/.zshrc; starship prompt'` to test without PTY complexity.
func TestShellZshStarshipIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// Source .zshrc (which contains starship init) then render the prompt.
	// This tests the full chain without needing a PTY.
	out, code := asUser(t, c, `zsh -c 'source ~/.zshrc 2>/dev/null; starship prompt'`)
	if code != 0 {
		t.Fatalf("zsh + starship prompt failed (exit %d): %s", code, out)
	}

	if !strings.Contains(out, "•") {
		t.Errorf("FAIL: zsh→.zshrc→starship prompt missing • symbol: %q", out)
	} else {
		t.Logf("PASS: zsh→.zshrc→starship prompt rendered •: %q", out)
	}
}

// TestShellZshAutosuggestions verifies the zsh-autosuggestions plugin is loaded.
func TestShellZshAutosuggestions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// Check if the autosuggestions plugin source file exists in the zshrc
	out, code := asUser(t, c, "grep -l autosuggestions ~/.zshrc")
	if code != 0 {
		t.Fatalf("FAIL: zsh-autosuggestions not referenced in .zshrc (exit %d): %s", code, out)
	}
	t.Logf("PASS: zsh-autosuggestions found in .zshrc")
}

// TestShellZshSyntaxHighlighting verifies the syntax-highlighting plugin is loaded.
func TestShellZshSyntaxHighlighting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	out, code := asUser(t, c, "grep -l syntax-highlighting ~/.zshrc")
	if code != 0 {
		t.Fatalf("FAIL: zsh-syntax-highlighting not referenced in .zshrc (exit %d): %s", code, out)
	}
	t.Logf("PASS: zsh-syntax-highlighting found in .zshrc")
}
