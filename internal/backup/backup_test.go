package backup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/backup"
)

func countBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "*-devcell-*-backup"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestBackup_FirstCall_CreatesFileAndBackup(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	if err := backup.Backup(dir, now); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	// .claude.json must exist
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err != nil {
		t.Error(".claude.json not created")
	}
	// At least one backup file
	backs := countBackups(t, dir)
	if len(backs) != 1 {
		t.Errorf("want 1 backup, got %d: %v", len(backs), backs)
	}
}

func TestBackup_FilenameMatchesPattern(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2025, 6, 20, 14, 30, 45, 0, time.UTC)
	if err := backup.Backup(dir, now); err != nil {
		t.Fatal(err)
	}
	backs := countBackups(t, dir)
	if len(backs) == 0 {
		t.Fatal("no backup files found")
	}
	name := filepath.Base(backs[0])
	if !strings.Contains(name, "-devcell-") || !strings.HasSuffix(name, "-backup") {
		t.Errorf("backup filename format unexpected: %q", name)
	}
	// Should contain the timestamp year
	if !strings.Contains(name, "2025") {
		t.Errorf("timestamp not in filename: %q", name)
	}
}

func TestBackup_KeepsOnly5(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		if err := backup.Backup(dir, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("Backup %d failed: %v", i, err)
		}
	}
	backs := countBackups(t, dir)
	if len(backs) != 5 {
		t.Errorf("want 5 backups, got %d: %v", len(backs), backs)
	}
}

func TestBackup_PreservesContent(t *testing.T) {
	dir := t.TempDir()
	claudeJSON := filepath.Join(dir, ".claude.json")
	content := `{"version": 1}`
	if err := os.WriteFile(claudeJSON, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := backup.Backup(dir, now); err != nil {
		t.Fatal(err)
	}
	backs := countBackups(t, dir)
	if len(backs) == 0 {
		t.Fatal("no backups found")
	}
	data, err := os.ReadFile(backs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("backup content mismatch: want %q, got %q", content, string(data))
	}
}

func TestBackup_CreatesDirIfAbsent(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "cellhome")
	now := time.Now()
	if err := backup.Backup(dir, now); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("cellhome dir not created")
	}
}

func TestBackup_OldestRemovedFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// Create 6, oldest is index 0
	for i := 0; i < 6; i++ {
		if err := backup.Backup(dir, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	backs := countBackups(t, dir)
	if len(backs) != 5 {
		t.Errorf("want 5 backups, got %d", len(backs))
	}
	// The oldest timestamp should be gone (2025-01-01T00:00:00)
	for _, b := range backs {
		if strings.Contains(b, "2025-01-01T00-00-00") {
			t.Errorf("oldest backup should have been removed: %q", b)
		}
	}
}
