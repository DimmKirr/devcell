package tart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNixVolumePath(t *testing.T) {
	got := NixVolumePath("/home/user")
	want := filepath.Join("/home/user", ".devcell", "darwin", "nix.img")
	if got != want {
		t.Errorf("NixVolumePath = %q, want %q", got, want)
	}
}

func TestEnsureNixVolume_CreatesSparseDisk(t *testing.T) {
	home := t.TempDir()

	imgPath, err := EnsureNixVolume(home)
	if err != nil {
		t.Fatalf("EnsureNixVolume failed: %v", err)
	}

	info, err := os.Stat(imgPath)
	if err != nil {
		t.Fatalf("image file not created: %v", err)
	}

	wantSize := int64(NixVolumeSizeGB) * 1024 * 1024 * 1024
	if info.Size() != wantSize {
		t.Errorf("image logical size = %d, want %d", info.Size(), wantSize)
	}

	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		allocBytes := sys.Blocks * 512
		if allocBytes > 1024*1024 {
			t.Errorf("expected sparse file (< 1MB on disk), got %d bytes allocated", allocBytes)
		}
	}
}

func TestEnsureNixVolume_Idempotent(t *testing.T) {
	home := t.TempDir()

	path1, err := EnsureNixVolume(home)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	info1, _ := os.Stat(path1)

	path2, err := EnsureNixVolume(home)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	info2, _ := os.Stat(path2)

	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("second call modified existing file — should be no-op")
	}
}

func TestGenerateNixVolumeMountScript_ContainsKeyElements(t *testing.T) {
	script := GenerateNixVolumeMountScript("main")

	for _, want := range []string{
		".devcell.json",
		"DevcellNix",
		"eraseDisk JHFS+",
		"synthetic.conf",
		"/nix",
		`"main"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script should contain %q", want)
		}
	}
}

func TestNixVolumeMetadataFormat(t *testing.T) {
	meta := map[string]any{
		"type":    "nix-store",
		"cell":    "main",
		"created": "2026-07-18T00:00:00Z",
		"sizeGB":  NixVolumeSizeGB,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["type"] != "nix-store" {
		t.Errorf("type = %v, want nix-store", parsed["type"])
	}
}
