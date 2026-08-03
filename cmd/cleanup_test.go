package main

import "testing"

// CELL-334: `cell cleanup` — reap GC roots no RUNNING container references.

func TestCleanupCmd_RegisteredOnRoot(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "cleanup" {
			return
		}
	}
	t.Fatal("`cell cleanup` command not registered on root")
}

func TestCleanupCmd_YesFlagExists(t *testing.T) {
	if cleanupCmd.Flags().Lookup("yes") == nil {
		t.Error("cleanup must support --yes to skip the confirmation prompt")
	}
}

// CELL-390: `cell claude --auto-cleanup` (etc.) opts into running the
// CELL-334 reaper at cell start. The flag is devcell's, not the agent's —
// it must never be forwarded to the inner binary.
func TestStripCellFlags_StripsAutoCleanup(t *testing.T) {
	got := stripCellFlags([]string{"--auto-cleanup", "prompt text"})
	for _, a := range got {
		if a == "--auto-cleanup" {
			t.Error("--auto-cleanup must be stripped from forwarded args")
		}
	}
	if len(got) != 1 || got[0] != "prompt text" {
		t.Errorf("non-cell args must survive stripping, got %v", got)
	}
}
