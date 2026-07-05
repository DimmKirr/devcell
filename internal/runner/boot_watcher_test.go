package runner_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-264 Layer 1 — pure ParseSentinelName helper.
//
// Filename convention from container fragments: <component>.<state>
//
//	mise.starting       — fragment 10-mise.sh starting work
//	mise.ready          — same fragment finished
//	home-directory.ready — kebab-case component name supported
//	boot.ready          — sealing event from entrypoint.sh
//
// Split is on the LAST '.' so future dotted component names (e.g.
// "nix.daemon.ready" → component="nix.daemon", state="ready") stay clean.

func TestParseSentinelName_BasicCase(t *testing.T) {
	comp, state, ok := runner.ParseSentinelName("mise.ready")
	if !ok || comp != "mise" || state != "ready" {
		t.Errorf("got (%q, %q, %v), want (mise, ready, true)", comp, state, ok)
	}
}

func TestParseSentinelName_KebabCaseComponent(t *testing.T) {
	comp, state, ok := runner.ParseSentinelName("home-directory.starting")
	if !ok || comp != "home-directory" || state != "starting" {
		t.Errorf("got (%q, %q, %v), want (home-directory, starting, true)", comp, state, ok)
	}
}

func TestParseSentinelName_NoDotReturnsFalse(t *testing.T) {
	_, _, ok := runner.ParseSentinelName("badname")
	if ok {
		t.Error("filename without '.' must return ok=false")
	}
}

func TestParseSentinelName_DottedComponentSplitsOnLast(t *testing.T) {
	// Future-proofing: a component name containing dots must keep them in
	// the component half — only the final segment is the state.
	comp, state, ok := runner.ParseSentinelName("nix.daemon.ready")
	if !ok || comp != "nix.daemon" || state != "ready" {
		t.Errorf("got (%q, %q, %v), want (nix.daemon, ready, true)", comp, state, ok)
	}
}

func TestParseSentinelName_EmptyReturnsFalse(t *testing.T) {
	_, _, ok := runner.ParseSentinelName("")
	if ok {
		t.Error("empty filename must return ok=false")
	}
}

func TestParseSentinelName_LeadingDotReturnsFalse(t *testing.T) {
	// ".ready" would imply an empty component — meaningless, ignore.
	_, _, ok := runner.ParseSentinelName(".ready")
	if ok {
		t.Error("filename with empty component must return ok=false")
	}
}

func TestParseSentinelName_TrailingDotReturnsFalse(t *testing.T) {
	// "mise." would imply an empty state — meaningless, ignore.
	_, _, ok := runner.ParseSentinelName("mise.")
	if ok {
		t.Error("filename with empty state must return ok=false")
	}
}

// ── BootDirWatcher tests (Layer 2) ─────────────────────────────────────

// drainBootEvents reads up to n events or returns the partial set on timeout.
func drainBootEvents(t *testing.T, ch <-chan runner.BootEvent, n int, timeout time.Duration) []runner.BootEvent {
	t.Helper()
	got := make([]runner.BootEvent, 0, n)
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
	return got
}

// TestBootDirWatcher_StartCreatesDir — Start must create the watched dir
// if it doesn't exist. Container's bind-mount semantics depend on the host
// path being present at docker-run time; we own the creation.
func TestBootDirWatcher_StartCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("test precondition: dir must not exist; got err=%v", err)
	}
	w := &runner.BootDirWatcher{}
	_, err := w.Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("dir must exist after Start: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Start must create a directory; got %v", info.Mode())
	}
}

// TestBootDirWatcher_DeliversCreateEvents — touching a sentinel file inside
// the watched dir must produce a BootEvent on the channel.
func TestBootDirWatcher_DeliversCreateEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	w := &runner.BootDirWatcher{}
	events, err := w.Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Mimic the container's `touch /tmp/devcell-boot/mise.ready`.
	for _, name := range []string{"mise.starting", "mise.ready"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create sentinel %s: %v", name, err)
		}
		_ = f.Close()
	}

	got := drainBootEvents(t, events, 2, 2*time.Second)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(got), got)
	}
	wantOrder := []struct{ Component, State string }{
		{"mise", "starting"},
		{"mise", "ready"},
	}
	for i, w := range wantOrder {
		if got[i].Component != w.Component || got[i].State != w.State {
			t.Errorf("event[%d]: got (%q, %q), want (%q, %q)",
				i, got[i].Component, got[i].State, w.Component, w.State)
		}
	}
}

// TestBootDirWatcher_IgnoresMalformedFilenames — filenames that don't
// parse (no '.', empty halves) must NOT produce events.
func TestBootDirWatcher_IgnoresMalformedFilenames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	w := &runner.BootDirWatcher{}
	events, err := w.Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Touch some malformed sentinels mixed with one valid one.
	for _, name := range []string{"badname", ".starting", "mise.", "mise.ready"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		_ = f.Close()
	}

	got := drainBootEvents(t, events, 4, 500*time.Millisecond)
	// Only the valid one should come through.
	if len(got) != 1 || got[0].Component != "mise" || got[0].State != "ready" {
		t.Errorf("expected exactly one (mise, ready) event, got %+v", got)
	}
}

// TestBootDirWatcher_CloseStopsAndChannelClosed — Close must terminate the
// watcher goroutine and close the events channel so consumers can range
// over it cleanly.
func TestBootDirWatcher_CloseStopsAndChannelClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	w := &runner.BootDirWatcher{}
	events, err := w.Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Channel must close so a consumer goroutine ranging over it exits.
	select {
	case _, ok := <-events:
		if ok {
			t.Errorf("events channel must be closed after Close")
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("events channel not closed within 500ms after Close")
	}
}

// TestBootDirWatcher_StartOnExistingDirIsIdempotent — Start on an
// already-existing dir succeeds (covers normal case where cmd/root.go
// pre-creates via os.MkdirAll for the bind-mount).
func TestBootDirWatcher_StartOnExistingDirIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	w := &runner.BootDirWatcher{}
	if _, err := w.Start(dir); err != nil {
		t.Errorf("Start on existing dir must succeed; got %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
}

// TestBootDirWatcher_EmitsInMtimeOrder — a single poll cycle may pick up
// multiple new sentinels; they must be emitted in creation-time order so
// the host's row sequence matches the container's fragment timeline. Bug
// without this fix: `mise.ready` arrives before `mise.starting` because
// `os.ReadDir` returns alphabetical and `r` < `s`. CELL-264 regression
// guard for the user-reported "Loading mise tools AFTER Mise ready" ordering.
func TestBootDirWatcher_EmitsInMtimeOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boot")
	// Pre-seed sentinels BEFORE Start so the initial scan picks them up
	// in one cycle (mirrors how a fast container batch writes everything
	// within one 100ms poll window).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create in deliberately non-alphabetical mtime order: ready first
	// then starting, with a small sleep so mtimes differ.
	createWithStamp := func(name string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		f.Close()
		time.Sleep(20 * time.Millisecond)
	}
	// Order: mise.starting → mise.ready → home.starting → home.ready
	// Alphabetical would give: home.ready, home.starting, mise.ready, mise.starting
	// Mtime-ordered must give the original creation sequence.
	createWithStamp("mise.starting")
	createWithStamp("mise.ready")
	createWithStamp("home.starting")
	createWithStamp("home.ready")

	w := &runner.BootDirWatcher{}
	events, err := w.Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	got := drainBootEvents(t, events, 4, 2*time.Second)
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(got), got)
	}
	wantOrder := []struct{ Component, State string }{
		{"mise", "starting"},
		{"mise", "ready"},
		{"home", "starting"},
		{"home", "ready"},
	}
	for i, w := range wantOrder {
		if got[i].Component != w.Component || got[i].State != w.State {
			t.Errorf("event[%d]: got (%q, %q), want (%q, %q) — events must arrive in mtime order, not alphabetical",
				i, got[i].Component, got[i].State, w.Component, w.State)
		}
	}
}
