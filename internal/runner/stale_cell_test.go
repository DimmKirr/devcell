package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-391: stale cell warning at start. Drift policy (2026-08-01):
// inform, not enforce — a nudge with `cell build --update`, never a gate.

// The probe (still read-only) must also report lock-drift datapoints from
// the *-meta files CELL-332 stamps: how many distinct nixpkgs revs are
// live, which is newest, and how many projects sit on it.
func TestNixHealthProbeScript_ReadsMetaRevs(t *testing.T) {
	for _, want := range []string{"*-meta", "nixpkgs="} {
		if !strings.Contains(runner.NixHealthProbeScript, want) {
			t.Errorf("probe must scan -meta files for nixpkgs revs, missing %q", want)
		}
	}
}

func TestParseNixStoreHealth_ParsesRevDatapoints(t *testing.T) {
	h, err := runner.ParseNixStoreHealth(
		"total=4 stale=0 hashes=2 generations=3 orphaned=0 revs=2 newest_rev=9f8e7d6abc newest_projects=3\n")
	if err != nil {
		t.Fatal(err)
	}
	if h.DistinctRevs != 2 || h.NewestRev != "9f8e7d6abc" || h.NewestProjects != 3 {
		t.Errorf("rev datapoints not parsed: %+v", h)
	}
}

// Pre-CELL-332 volumes have no -meta files; the old datapoint line (no rev
// fields) must still parse — missing keys are zero values, not errors.
func TestParseNixStoreHealth_RevFieldsOptional(t *testing.T) {
	h, err := runner.ParseNixStoreHealth("total=1 stale=0 hashes=1 generations=1 orphaned=0\n")
	if err != nil {
		t.Fatal(err)
	}
	if h.DistinctRevs != 0 || h.NewestRev != "" {
		t.Errorf("missing rev fields must be zero values: %+v", h)
	}
}

func TestProjectNixpkgsRev_ReadsScaffoldedLock(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, ".devcell")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"nodes":{"nixpkgs":{"locked":{"rev":"4a1b2c3deadbeef"}}}}`
	if err := os.WriteFile(filepath.Join(lockDir, "flake.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	rev, err := runner.ProjectNixpkgsRev(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rev != "4a1b2c3deadbeef" {
		t.Errorf("want 4a1b2c3deadbeef, got %q", rev)
	}
}

func TestProjectNixpkgsRev_MissingLockIsEmptyNotError(t *testing.T) {
	rev, err := runner.ProjectNixpkgsRev(t.TempDir())
	if err != nil {
		t.Fatalf("missing lock must degrade silently, got %v", err)
	}
	if rev != "" {
		t.Errorf("want empty rev, got %q", rev)
	}
}

func TestStaleCellWarning_BehindNewestWarns(t *testing.T) {
	h := runner.NixStoreHealth{DistinctRevs: 2, NewestRev: "9f8e7d6abcdef", NewestProjects: 3}
	msg, stale := runner.StaleCellWarning("4a1b2c3deadbeef", h)
	if !stale {
		t.Fatal("project behind newest volume rev must warn")
	}
	for _, want := range []string{
		"4a1b2c3", "9f8e7d6", "3 project", "parallel reality",
		"cell build --update", "Continue anyway? [Y/n]",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning missing %q:\n%s", want, msg)
		}
	}
}

func TestStaleCellWarning_OnNewestIsSilent(t *testing.T) {
	h := runner.NixStoreHealth{DistinctRevs: 1, NewestRev: "9f8e7d6abcdef"}
	if _, stale := runner.StaleCellWarning("9f8e7d6abcdef", h); stale {
		t.Error("project on the newest rev must not warn")
	}
}

// Detection failure degrades to silence — never a prompt, never fatal.
func TestStaleCellWarning_UnknownRevsAreSilent(t *testing.T) {
	if _, stale := runner.StaleCellWarning("", runner.NixStoreHealth{NewestRev: "abc"}); stale {
		t.Error("unknown project rev must not warn")
	}
	if _, stale := runner.StaleCellWarning("abc", runner.NixStoreHealth{}); stale {
		t.Error("no volume rev data must not warn")
	}
}

// ConfirmProceed is the default-YES twin of ConfirmDestructive: a nudge,
// not a gate. Enter / y / anything-but-n proceeds; only n/no aborts.
// Non-TTY always proceeds after printing the warning — never blocks CI.
func TestConfirmProceed_EnterProceeds(t *testing.T) {
	var out strings.Builder
	if !runner.ConfirmProceed(&out, strings.NewReader("\n"), true, "WARN") {
		t.Error("bare Enter must proceed (default yes)")
	}
}

func TestConfirmProceed_NoAborts(t *testing.T) {
	var out strings.Builder
	if runner.ConfirmProceed(&out, strings.NewReader("n\n"), true, "WARN") {
		t.Error("answering n must abort")
	}
}

func TestConfirmProceed_NonTTYProceedsWithWarning(t *testing.T) {
	var out strings.Builder
	if !runner.ConfirmProceed(&out, strings.NewReader(""), false, "WARN") {
		t.Error("non-TTY must proceed unconditionally")
	}
	if !strings.Contains(out.String(), "WARN") {
		t.Error("non-TTY must still print the warning")
	}
}
