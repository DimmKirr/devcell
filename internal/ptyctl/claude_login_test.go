package ptyctl_test

import (
	"context"
	"os"
	osexec "os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/ptyctl"
)

// TestClaudeLogin_PTYControl spawns `claude` under a virtual terminal with a
// fresh HOME (no cached auth), navigates the onboarding flow (theme → login),
// and extracts the browser login URL. Proof-of-concept for PTY-driven control
// of interactive TUI apps.
func TestClaudeLogin_PTYControl(t *testing.T) {
	if testing.Short() {
		t.Skip("requires interactive claude binary on PATH")
	}

	claudeBin, err := findClaude()
	if err != nil {
		t.Skip("claude not on PATH:", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	freshHome := t.TempDir()

	term, err := ptyctl.StartWithOptions(ctx,
		[]ptyctl.StartOption{
			ptyctl.WithEnv("HOME=" + freshHome),
		},
		claudeBin,
	)
	if err != nil {
		t.Fatalf("start claude: %v", err)
	}
	defer term.Close()

	// Step 1: Claude shows a theme picker on first run. Wait for it, then
	// press Enter to accept the default ("Dark mode").
	_, err = term.WaitFor(ctx, "Choose the text style")
	if err != nil {
		t.Fatalf("theme picker never appeared: %v", err)
	}
	t.Logf("=== Theme picker ===\n%s", term.Screen())

	time.Sleep(300 * time.Millisecond)
	if err := term.Enter(); err != nil {
		t.Fatalf("send Enter (theme): %v", err)
	}

	// Step 2: After theme selection, Claude shows the login / auth screen.
	// Wait for it to render.
	matched, _, err := term.WaitForAny(ctx,
		"Login",
		"login",
		"Sign in",
		"Anthropic",
		"authenticate",
		"Account",
		"API key",
	)
	if err != nil {
		t.Fatalf("login screen never appeared: %v\nscreen:\n%s", err, term.Screen())
	}
	t.Logf("=== Login screen (matched %q) ===\n%s", matched, term.Screen())

	// Step 3: Press Enter to select the default login option.
	time.Sleep(300 * time.Millisecond)
	if err := term.Enter(); err != nil {
		t.Fatalf("send Enter (login): %v", err)
	}

	// Step 4: Wait for a URL to appear (browser auth URL).
	urlCtx, urlCancel := context.WithTimeout(ctx, 15*time.Second)
	defer urlCancel()

	_, err = term.WaitFor(urlCtx, "http")
	if err != nil {
		t.Fatalf("no URL after selecting login: %v\nscreen:\n%s", err, term.Screen())
	}

	finalScreen := term.Screen()
	t.Logf("=== Screen with login URL ===\n%s", finalScreen)

	urlRe := regexp.MustCompile(`https?://[^\s]+`)
	urls := urlRe.FindAllString(finalScreen, -1)
	if len(urls) == 0 {
		t.Fatal("no URL found on screen after login")
	}

	t.Logf("Login URL: %s", urls[0])
}

func findClaude() (string, error) {
	for _, p := range []string{
		"/opt/devcell/.local/state/nix/profiles/profile/bin/claude",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := osexec.LookPath("claude")
	if err != nil {
		return "", err
	}
	return p, nil
}
