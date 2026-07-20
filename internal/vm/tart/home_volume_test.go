package tart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCellHomePath(t *testing.T) {
	got := CellHome("/home/user", "main")
	want := filepath.Join("/home/user", ".devcell", "main")
	if got != want {
		t.Errorf("CellHome = %q, want %q", got, want)
	}
}

func TestCellHomePath_DifferentCells(t *testing.T) {
	pathMain := CellHome("/home/user", "main")
	pathWork := CellHome("/home/user", "work")

	if pathMain == pathWork {
		t.Error("different cells should have different CellHome paths")
	}
	if !strings.Contains(pathMain, "main") {
		t.Errorf("path should contain cell name 'main': %s", pathMain)
	}
	if !strings.Contains(pathWork, "work") {
		t.Errorf("path should contain cell name 'work': %s", pathWork)
	}
}

func TestEnsureHomeDir_CreatesDirectory(t *testing.T) {
	home := t.TempDir()

	dirPath, err := EnsureHomeDir(home, "main")
	if err != nil {
		t.Fatalf("EnsureHomeDir failed: %v", err)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected a directory, got a file")
	}

	want := filepath.Join(home, ".devcell", "main")
	if dirPath != want {
		t.Errorf("EnsureHomeDir path = %q, want %q", dirPath, want)
	}
}

func TestEnsureHomeDir_Idempotent(t *testing.T) {
	home := t.TempDir()

	path1, err := EnsureHomeDir(home, "main")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	marker := filepath.Join(path1, "test-marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path2, err := EnsureHomeDir(home, "main")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Error("second call destroyed existing contents")
	}
}

func TestGenerateHomeMountScript_ContainsKeyElements(t *testing.T) {
	script := GenerateHomeMountScript("main", "dmitry")

	for _, want := range []string{
		"home",
		"/Users/dmitry",
		"automount",
		"mount_virtiofs",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script should contain %q", want)
		}
	}
}

func TestGenerateHomeMountScript_NoAPFS(t *testing.T) {
	script := GenerateHomeMountScript("main", "dmitry")
	if strings.Contains(script, "APFS") || strings.Contains(script, "eraseDisk") {
		t.Error("home mount script should not contain APFS or eraseDisk — VirtioFS, not disk image")
	}
}
