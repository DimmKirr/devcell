package runner_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CELL-263 fragment-presence tests.
//
// Unit tests for the sd_notify protocol prove the parser/listener/consumer
// primitives work. They DON'T prove each fragment actually calls notify —
// a typo, missing call, or accidental deletion would ship silently.
//
// These tests close that gap with static checks on the fragment files:
//
//   - Every work-doing fragment must call `notify "STATUS=..."` at least once
//     so the host sees something happened in the container.
//   - Every fragment that ends in a "ready" state must call `notify "STATUS=*
//     ready"` at the end so the host knows the work completed.
//
// Two fragments are exempt:
//   - 00-notify.sh — defines the notify() helper itself, has nothing to report.
//   - 01-locale.sh — generated inline in base.nix (not a file), too small to track.
//
// If these tests fail, the most likely fix is to add the missing notify call.
// The test names the fragment + the missing pattern.

func fragmentDir(t *testing.T) string {
	t.Helper()
	// repo root is two directories up from this test file's package dir.
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return filepath.Join(repo, "nixhome", "modules", "fragments")
}

// workFragments are fragments that do meaningful boot work; each must
// report at least one STATUS line.
var workFragments = []string{
	"04-nix-daemon.sh",
	"05-shell-rc.sh",
	"06-nix-ldpath.sh",
	"10-mise.sh",
	"20-homedir.sh",
	"21-secrets.sh",
	"22-chromium-singleton.sh",
	"30-claude.sh",
	"30-codex.sh",
	"30-gemini.sh",
	"30-opencode.sh",
	"40-postgres.sh",
	"50-gui.sh",
}

func TestFragmentsCallNotify(t *testing.T) {
	// CELL-264 format: notify <component>.<state>  (no quotes, no STATUS= prefix).
	// Catches the "ship a fragment without notify calls" bug class.
	notifyCallRe := regexp.MustCompile(`notify\s+[a-zA-Z][\w.-]*\.(starting|ready)`)
	for _, name := range workFragments {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(fragmentDir(t), name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !notifyCallRe.Match(data) {
				t.Errorf("%s: missing `notify <component>.(starting|ready)` call — host won't see progress for this fragment. "+
					"Add a notify line at the start of meaningful work.", name)
			}
		})
	}
}

// TestFragmentsHaveReadyMarker — every work fragment must emit a terminal
// `notify <component>.ready`. Catches half-wired fragments (starting but no
// ready). This is what the host watcher uses to seal each row.
func TestFragmentsHaveReadyMarker(t *testing.T) {
	readyRe := regexp.MustCompile(`notify\s+[a-zA-Z][\w.-]*\.ready`)
	for _, name := range workFragments {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(fragmentDir(t), name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !readyRe.Match(data) {
				t.Errorf("%s: missing terminal `notify <component>.ready` call — host won't know when this fragment finishes.", name)
			}
		})
	}
}

// TestNotifyHelperIsRegistered verifies 00-notify.sh is wired into base.nix.
// This is the bug class that bit us during smoke-testing: fragment file
// existed on disk, but no home.file entry copied it into the image.
// Without 00-notify.sh in /etc/devcell/entrypoint.d/, every later fragment's
// notify call dies with "command not found".
func TestNotifyHelperIsRegistered(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	baseNix := filepath.Join(repo, "nixhome", "modules", "base.nix")
	data, err := os.ReadFile(baseNix)
	if err != nil {
		t.Fatalf("read %s: %v", baseNix, err)
	}
	if !strings.Contains(string(data), "00-notify.sh") {
		t.Errorf("base.nix must register 00-notify.sh as a home.file entry — without it, the helper never ships in the image and every later fragment errors with `notify: command not found`")
	}
}

// TestNotifyHelperDefinesFunction is a sanity check on the helper itself —
// guards against accidental deletion of the notify() function.
func TestNotifyHelperDefinesFunction(t *testing.T) {
	path := filepath.Join(fragmentDir(t), "00-notify.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), "notify()") {
		t.Errorf("00-notify.sh must define a `notify()` shell function")
	}
}

// TestNotifyHelperUsesTouchNotSocat — CELL-264 swaps the helper from
// socat→unix-socket (sd_notify, CELL-263) to plain `touch` in a bind-mounted
// directory. Static check guards against regressions and validates the
// sd_notify carcass is fully gone.
func TestNotifyHelperUsesTouchNotSocat(t *testing.T) {
	path := filepath.Join(fragmentDir(t), "00-notify.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := string(data)

	// Negative checks — sd_notify carcass markers must be GONE.
	for _, banned := range []string{
		"socat",
		"UNIX-SENDTO",
		"UDP-SENDTO",
		"NOTIFY_SOCKET",
	} {
		if strings.Contains(s, banned) {
			t.Errorf("00-notify.sh must NOT contain %q (sd_notify carcass — should be removed under CELL-264)", banned)
		}
	}

	// Positive checks — the new transport must be present.
	if !strings.Contains(s, "touch") {
		t.Errorf("00-notify.sh must use `touch` for sentinel files (CELL-264 transport)")
	}
	if !strings.Contains(s, "DEVCELL_BOOT_DIR") {
		t.Errorf("00-notify.sh must reference $DEVCELL_BOOT_DIR (the CELL-264 boot-progress dir env var)")
	}
}

// TestEntrypointEmitsReady — the host consumer seals its loop on the
// boot.ready sentinel (CELL-264). If entrypoint.sh doesn't emit it before
// exec'ing into the child binary, the host hangs rendering the in-flight
// row forever.
func TestEntrypointEmitsReady(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ep := filepath.Join(repo, "nixhome", "entrypoint.sh")
	data, err := os.ReadFile(ep)
	if err != nil {
		t.Fatalf("read %s: %v", ep, err)
	}
	if !regexp.MustCompile(`notify\s+boot\.ready`).Match(data) {
		t.Errorf("entrypoint.sh must emit `notify boot.ready` before exec — without it, the host consumer never seals the final row.")
	}
}
