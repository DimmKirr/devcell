package main

import (
	"os"
	"path/filepath"
	"testing"
)

// CELL-74 — fingerprint.json moved from $CellHome/playwright-fingerprint.json
// to $CellHome/.playwright/fingerprint.json. These tests pin the new path and
// the auto-mkdir behavior that lets first-run callers write without manual
// `mkdir -p`.

func TestSavePlaywrightFingerprint_WritesToPlaywrightSubdir(t *testing.T) {
	cellHome := t.TempDir()
	fp := &playwrightFingerprint{
		UserAgent:  "test-ua",
		Platform:   "MacIntel",
		UAPlatform: "macOS",
		Version:    "999.0.0.0",
	}

	savePlaywrightFingerprint(cellHome, fp)

	newPath := filepath.Join(cellHome, ".playwright", "fingerprint.json")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected fingerprint at %s after save, got: %v", newPath, err)
	}

	// Must NOT exist at the legacy flat path.
	legacyPath := filepath.Join(cellHome, "playwright-fingerprint.json")
	if _, err := os.Stat(legacyPath); err == nil {
		t.Errorf("fingerprint accidentally also written to legacy path %s", legacyPath)
	}
}

func TestReadPlaywrightFingerprint_ReadsFromPlaywrightSubdir(t *testing.T) {
	cellHome := t.TempDir()
	fp := &playwrightFingerprint{UserAgent: "ua-round-trip", Version: "1.2.3"}

	savePlaywrightFingerprint(cellHome, fp)
	got := readPlaywrightFingerprint(cellHome)

	if got == nil {
		t.Fatalf("readPlaywrightFingerprint returned nil after save")
	}
	if got.UserAgent != "ua-round-trip" {
		t.Errorf("UserAgent = %q, want ua-round-trip", got.UserAgent)
	}
	if got.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", got.Version)
	}
}

// First save against an otherwise-empty cellHome must create the .playwright/
// subdir (instead of erroring with "no such file or directory").
func TestSavePlaywrightFingerprint_CreatesParentDir(t *testing.T) {
	cellHome := t.TempDir()
	subdir := filepath.Join(cellHome, ".playwright")
	if _, err := os.Stat(subdir); !os.IsNotExist(err) {
		t.Fatalf("test setup wrong: .playwright already exists: %v", err)
	}

	savePlaywrightFingerprint(cellHome, &playwrightFingerprint{UserAgent: "ua"})

	if _, err := os.Stat(filepath.Join(subdir, "fingerprint.json")); err != nil {
		t.Errorf("fingerprint not at %s after save: %v", subdir, err)
	}
}
