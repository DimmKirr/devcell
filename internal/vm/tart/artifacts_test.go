package tart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightCheck_DarwinARM64(t *testing.T) {
	err := PreflightCheck("darwin", "arm64")
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPreflightCheck_Linux(t *testing.T) {
	err := PreflightCheck("linux", "amd64")
	if err == nil {
		t.Fatal("expected error for linux host")
	}
	if !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("error should mention macOS, got: %v", err)
	}
}

func TestPreflightCheck_DarwinIntel(t *testing.T) {
	err := PreflightCheck("darwin", "amd64")
	if err == nil {
		t.Fatal("expected error for Intel Mac")
	}
	if !strings.Contains(err.Error(), "Apple Silicon") {
		t.Fatalf("error should mention Apple Silicon, got: %v", err)
	}
}

func TestNewArtifactPaths_Layout(t *testing.T) {
	ap := NewArtifactPaths("/Users/bob", "main")

	wantDir := filepath.Join("/Users/bob", ".devcell", "main", "darwin")
	if ap.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", ap.Dir, wantDir)
	}
	if ap.Disk != filepath.Join(wantDir, "disk.img") {
		t.Fatalf("Disk = %q, want disk.img under %q", ap.Disk, wantDir)
	}
	if ap.AuxStorage != filepath.Join(wantDir, "aux-storage.img") {
		t.Fatalf("AuxStorage = %q, want aux-storage.img under %q", ap.AuxStorage, wantDir)
	}
	if ap.HWModel != filepath.Join(wantDir, "hardware-model.json") {
		t.Fatalf("HWModel = %q, want hardware-model.json under %q", ap.HWModel, wantDir)
	}
	if ap.MachineID != filepath.Join(wantDir, "machine-id.json") {
		t.Fatalf("MachineID = %q, want machine-id.json under %q", ap.MachineID, wantDir)
	}
}

func TestNewTemplatePaths_Layout(t *testing.T) {
	tp := NewTemplatePaths("/Users/bob", "ultimate", nil)
	wantDir := filepath.Join("/Users/bob", ".devcell", "darwin", "ultimate")
	if tp.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", tp.Dir, wantDir)
	}
	if tp.Disk != filepath.Join(wantDir, "disk.img") {
		t.Fatalf("Disk = %q, want disk.img under %q", tp.Disk, wantDir)
	}
	if tp.AuxStorage != filepath.Join(wantDir, "aux-storage.img") {
		t.Fatalf("AuxStorage = %q", tp.AuxStorage)
	}
	if tp.HWModel != filepath.Join(wantDir, "hardware-model.json") {
		t.Fatalf("HWModel = %q", tp.HWModel)
	}
	if tp.MachineID != filepath.Join(wantDir, "machine-id.json") {
		t.Fatalf("MachineID = %q", tp.MachineID)
	}
}

func TestNewTemplatePaths_WithModules(t *testing.T) {
	tp := NewTemplatePaths("/Users/bob", "dev", []string{"plex", "linear"})
	tag := StackTag("dev", []string{"plex", "linear"})
	wantDir := filepath.Join("/Users/bob", ".devcell", "darwin", tag)
	if tp.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", tp.Dir, wantDir)
	}
}

func TestNewCellSSHPaths_Layout(t *testing.T) {
	sp := NewCellSSHPaths("/Users/bob", "main")
	wantDir := filepath.Join("/Users/bob", ".devcell", "main", ".ssh")
	if sp.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", sp.Dir, wantDir)
	}
	if sp.PrivateKey != filepath.Join(wantDir, "id_ed25519") {
		t.Fatalf("PrivateKey = %q", sp.PrivateKey)
	}
	if sp.PublicKey != filepath.Join(wantDir, "id_ed25519.pub") {
		t.Fatalf("PublicKey = %q", sp.PublicKey)
	}
}

func TestCellHome(t *testing.T) {
	got := CellHome("/Users/bob", "main")
	want := filepath.Join("/Users/bob", ".devcell", "main")
	if got != want {
		t.Fatalf("CellHome = %q, want %q", got, want)
	}
}

func TestArtifactPaths_Exists_AllPresent(t *testing.T) {
	dir := t.TempDir()
	ap := ArtifactPaths{
		Dir:        dir,
		Disk:       filepath.Join(dir, "disk.img"),
		AuxStorage: filepath.Join(dir, "aux-storage.img"),
		HWModel:    filepath.Join(dir, "hardware-model.json"),
		MachineID:  filepath.Join(dir, "machine-id.json"),
	}
	for _, f := range []string{ap.Disk, ap.AuxStorage, ap.HWModel, ap.MachineID} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !ap.Exists() {
		t.Fatal("Exists() = false, want true when all files present")
	}
}

func TestArtifactPaths_Exists_MissingDisk(t *testing.T) {
	dir := t.TempDir()
	ap := ArtifactPaths{
		Dir:        dir,
		Disk:       filepath.Join(dir, "disk.img"),
		AuxStorage: filepath.Join(dir, "aux-storage.img"),
		HWModel:    filepath.Join(dir, "hardware-model.json"),
		MachineID:  filepath.Join(dir, "machine-id.json"),
	}
	// Create all except disk.img
	for _, f := range []string{ap.AuxStorage, ap.HWModel, ap.MachineID} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if ap.Exists() {
		t.Fatal("Exists() = true, want false when disk.img missing")
	}
}

func TestArtifactPaths_MissingFiles_None(t *testing.T) {
	dir := t.TempDir()
	ap := ArtifactPaths{
		Dir:        dir,
		Disk:       filepath.Join(dir, "disk.img"),
		AuxStorage: filepath.Join(dir, "aux-storage.img"),
		HWModel:    filepath.Join(dir, "hardware-model.json"),
		MachineID:  filepath.Join(dir, "machine-id.json"),
	}
	for _, f := range []string{ap.Disk, ap.AuxStorage, ap.HWModel, ap.MachineID} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missing := ap.MissingFiles()
	if len(missing) != 0 {
		t.Fatalf("MissingFiles() = %v, want empty", missing)
	}
}

func TestArtifactPaths_MissingFiles_Some(t *testing.T) {
	dir := t.TempDir()
	ap := ArtifactPaths{
		Dir:        dir,
		Disk:       filepath.Join(dir, "disk.img"),
		AuxStorage: filepath.Join(dir, "aux-storage.img"),
		HWModel:    filepath.Join(dir, "hardware-model.json"),
		MachineID:  filepath.Join(dir, "machine-id.json"),
	}
	// Create only aux-storage.img and hardware-model.json
	for _, f := range []string{ap.AuxStorage, ap.HWModel} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missing := ap.MissingFiles()
	if len(missing) != 2 {
		t.Fatalf("MissingFiles() returned %d items, want 2: %v", len(missing), missing)
	}
	want := map[string]bool{"disk.img": true, "machine-id.json": true}
	for _, m := range missing {
		if !want[m] {
			t.Errorf("unexpected missing file: %s", m)
		}
	}
}

func TestLoadArtifacts_Valid(t *testing.T) {
	home := t.TempDir()
	cellName := "testcell"
	dir := ArtifactDir(home, cellName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"disk.img", "aux-storage.img", "hardware-model.json", "machine-id.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ap, err := LoadArtifacts(home, cellName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ap.Dir != dir {
		t.Fatalf("Dir = %q, want %q", ap.Dir, dir)
	}
}

func TestIPSWCacheDir(t *testing.T) {
	got := IPSWCacheDir("/Users/bob")
	want := filepath.Join("/Users/bob", ".devcell", "cache", "ipsw")
	if got != want {
		t.Fatalf("IPSWCacheDir = %q, want %q", got, want)
	}
}

func TestIPSWCachePath(t *testing.T) {
	got := IPSWCachePath("/Users/bob")
	want := filepath.Join("/Users/bob", ".devcell", "cache", "ipsw", "restore.ipsw")
	if got != want {
		t.Fatalf("IPSWCachePath = %q, want %q", got, want)
	}
}

func TestIPSWCacheDir_Independent_Of_CellName(t *testing.T) {
	a := IPSWCacheDir("/home/test")
	b := IPSWCacheDir("/home/test")
	if a != b {
		t.Fatalf("IPSWCacheDir should be independent of cell name")
	}
	if strings.Contains(a, "main") || strings.Contains(a, "darwin") {
		t.Fatalf("IPSWCacheDir should not contain cell-specific paths, got %q", a)
	}
}

func TestScreenshotDir(t *testing.T) {
	got := ScreenshotDir("/Users/bob/dev/myproject")
	want := filepath.Join("/Users/bob/dev/myproject", ".devcell", "debug", "screenshots")
	if got != want {
		t.Fatalf("ScreenshotDir = %q, want %q", got, want)
	}
}

func TestLoadArtifacts_Missing(t *testing.T) {
	home := t.TempDir()
	_, err := LoadArtifacts(home, "nocell")
	if err == nil {
		t.Fatal("expected error when no files exist")
	}
	if !strings.Contains(err.Error(), "cell init --engine=tart") {
		t.Fatalf("error should mention 'cell init --engine=tart', got: %v", err)
	}
}
