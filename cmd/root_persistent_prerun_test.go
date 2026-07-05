package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestPersistentPreRun_SetsRunnerStackFromConfig pins the fix for the bug
// where `cell build` produced devcell-user:base-pure even when .devcell.toml
// said stack="ultimate". Root cause: runner.Stack was only set in rootCmd's
// RunE (which is skipped when a subcommand is invoked), not in
// PersistentPreRun (which fires for every subcommand).
//
// Test: with a .devcell.toml in cwd specifying stack="ultimate", invoking
// rootCmd's PersistentPreRun must leave runner.Stack == "ultimate".
func TestPersistentPreRun_SetsRunnerStackFromConfig(t *testing.T) {
	// Save + restore globals so we don't bleed into other tests.
	origStack := runner.Stack
	origModules := runner.Modules
	origPerSession := runner.PerCellImage
	t.Cleanup(func() {
		runner.Stack = origStack
		runner.Modules = origModules
		runner.PerCellImage = origPerSession
	})

	// Build a temp project with .devcell.toml stack="ultimate".
	tmp := t.TempDir()
	tomlPath := filepath.Join(tmp, ".devcell.toml")
	if err := os.WriteFile(tomlPath, []byte(`[cell]
stack = "ultimate"
modules = ["desktop"]
`), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// PersistentPreRun reads cwd to find .devcell.toml.
	prevWd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	// Reset to package defaults so we can prove PersistentPreRun changed them.
	runner.Stack = ""
	runner.Modules = nil

	// Fire the actual PersistentPreRun from rootCmd. We don't go through
	// Execute() because that would also run a subcommand; PersistentPreRun
	// is what we're pinning.
	rootCmd.PersistentPreRun(rootCmd, []string{})

	if runner.Stack != "ultimate" {
		t.Errorf("runner.Stack = %q, want %q (PersistentPreRun didn't pick up .devcell.toml)",
			runner.Stack, "ultimate")
	}
	if len(runner.Modules) != 1 || runner.Modules[0] != "desktop" {
		t.Errorf("runner.Modules = %v, want [desktop]", runner.Modules)
	}

	// Downstream: UserImageTagPure() must now produce the ultimate tag,
	// not fall back to base-pure. This is the actual user-visible bug.
	if got := runner.UserImageTagPure(); got == "devcell-user:base-pure" {
		t.Errorf("UserImageTagPure() = %q — fell back to base-pure despite stack=ultimate", got)
	}
}

// TestPersistentPreRun_NoConfig_LeavesDefaults verifies the silent-skip
// behavior: if there's no .devcell.toml (e.g. user runs `cell --help` from
// a stray directory), PersistentPreRun should not panic and should not
// overwrite an already-set Stack with garbage.
func TestPersistentPreRun_NoConfig_LeavesDefaults(t *testing.T) {
	origStack := runner.Stack
	t.Cleanup(func() { runner.Stack = origStack })

	tmp := t.TempDir() // empty, no .devcell.toml
	prevWd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	runner.Stack = ""

	// Should not panic. cfg.LoadFromOS may pull stack from a global
	// ~/.config/devcell/devcell.toml when no local .devcell.toml exists,
	// so we don't assert a specific value — only that the call completed
	// and left Stack with *something* (either the global value or "base").
	rootCmd.PersistentPreRun(rootCmd, []string{})

	if runner.Stack == "" {
		// ResolvedStack() defaults to "base" when nothing is set, so the
		// only way to land back at empty is if PersistentPreRun bailed
		// without setting the global — which is a regression.
		t.Error("runner.Stack still empty after PersistentPreRun — config load was skipped")
	}
}
