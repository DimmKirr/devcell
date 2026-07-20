package tart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildConfig_Defaults(t *testing.T) {
	c := BuildConfig{HomeDir: "/home/test"}
	c.ApplyDefaults()

	if c.CellName != "main" {
		t.Errorf("CellName = %q, want main", c.CellName)
	}
	if c.Stack != "base" {
		t.Errorf("Stack = %q, want base", c.Stack)
	}
	if c.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", c.CPUs)
	}
	if c.MemoryGB != 4 {
		t.Errorf("MemoryGB = %d, want 4", c.MemoryGB)
	}
	if c.SSHPort != 22 {
		t.Errorf("SSHPort = %d, want 22", c.SSHPort)
	}
	if c.Username != "admin" {
		t.Errorf("Username = %q, want admin", c.Username)
	}
}

func TestBuildConfig_ImagePaths(t *testing.T) {
	c := BuildConfig{
		HomeDir:  "/Users/test",
		CellName: "dev",
		Stack:    "ultimate",
	}

	want := "/Users/test/.devcell/dev/darwin/disk.img"
	if got := c.BaseImagePath(); got != want {
		t.Errorf("BaseImagePath = %q, want %q", got, want)
	}

	want = "/Users/test/.devcell/dev/darwin/disk-build.img"
	if got := c.BuildImagePath(); got != want {
		t.Errorf("BuildImagePath = %q, want %q", got, want)
	}

	want = "/Users/test/.devcell/dev/darwin/disk-ultimate.img"
	if got := c.FinalImagePath(); got != want {
		t.Errorf("FinalImagePath = %q, want %q", got, want)
	}
}

func TestBuildPreflight_MissingBase(t *testing.T) {
	err := BuildPreflight("/nonexistent/disk.img")
	if err == nil {
		t.Fatal("expected error for missing base disk")
	}
	if !strings.Contains(err.Error(), "cell init") {
		t.Errorf("error %q should mention 'cell init'", err)
	}
}

func TestBuildPreflight_OK(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "disk.img")
	os.WriteFile(base, []byte("disk"), 0644)

	if err := BuildPreflight(base); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSparseCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.img")
	dst := filepath.Join(dir, "dst.img")

	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}
	os.WriteFile(src, data, 0644)

	if err := SparseCopy(src, dst); err != nil {
		t.Fatalf("SparseCopy: %v", err)
	}

	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if dstInfo.Size() != 4096 {
		t.Errorf("dst size = %d, want 4096", dstInfo.Size())
	}

	dstData, _ := os.ReadFile(dst)
	for i, b := range dstData {
		if b != data[i] {
			t.Fatalf("byte mismatch at %d: got %d, want %d", i, b, data[i])
		}
	}
}

func TestSparseCopyWithProgress(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.img")
	dst := filepath.Join(dir, "dst.img")

	// 4MB file — triggers multiple 1MB chunk reads
	data := make([]byte, 4*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	os.WriteFile(src, data, 0644)

	var reports []CopyProgress
	err := SparseCopyWithProgress(src, dst, func(p CopyProgress) {
		reports = append(reports, p)
	})
	if err != nil {
		t.Fatalf("SparseCopyWithProgress: %v", err)
	}

	if len(reports) == 0 {
		t.Fatal("expected at least one progress report")
	}

	last := reports[len(reports)-1]
	if last.BytesCopied != last.TotalBytes {
		t.Errorf("final report: BytesCopied=%d, TotalBytes=%d — should be equal",
			last.BytesCopied, last.TotalBytes)
	}
	if last.TotalBytes != int64(len(data)) {
		t.Errorf("TotalBytes = %d, want %d", last.TotalBytes, len(data))
	}

	// Verify data integrity
	dstData, _ := os.ReadFile(dst)
	if len(dstData) != len(data) {
		t.Fatalf("dst size = %d, want %d", len(dstData), len(data))
	}
	for i, b := range dstData {
		if b != data[i] {
			t.Fatalf("byte mismatch at %d: got %d, want %d", i, b, data[i])
		}
	}
}

func TestSparseCopyWithProgress_NilCallback(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.img")
	dst := filepath.Join(dir, "dst.img")

	os.WriteFile(src, []byte("hello"), 0644)

	if err := SparseCopyWithProgress(src, dst, nil); err != nil {
		t.Fatalf("SparseCopyWithProgress(nil): %v", err)
	}

	dstData, _ := os.ReadFile(dst)
	if string(dstData) != "hello" {
		t.Errorf("dst = %q, want %q", string(dstData), "hello")
	}
}

func TestFormatCopyProgress(t *testing.T) {
	tests := []struct {
		p    CopyProgress
		want string
	}{
		{CopyProgress{BytesCopied: 0, TotalBytes: 100 * 1024 * 1024}, "Copying 0MB / 100MB (0%)"},
		{CopyProgress{BytesCopied: 15 * 1024 * 1024, TotalBytes: 100 * 1024 * 1024}, "Copying 15MB / 100MB (15%)"},
		{CopyProgress{BytesCopied: 100 * 1024 * 1024, TotalBytes: 100 * 1024 * 1024}, "Copying 100MB / 100MB (100%)"},
	}
	for _, tt := range tests {
		got := tt.p.String()
		if got != tt.want {
			t.Errorf("CopyProgress.String() = %q, want %q", got, tt.want)
		}
	}
}

func TestBuildProvisionCommands_HasNixDarwinSwitch(t *testing.T) {
	cmds := BuildProvisionCommands("ultimate", nil)
	found := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "nix-darwin") && strings.Contains(cmd, "ultimate") {
			found = true
		}
	}
	if !found {
		t.Errorf("provision commands should contain nix-darwin switch with 'ultimate': %v", cmds)
	}
}

func TestBuildConfig_FinalImagePath_WithModules(t *testing.T) {
	c := BuildConfig{
		HomeDir:  "/Users/test",
		CellName: "dev",
		Stack:    "dev",
		Modules:  []string{"plex", "linear"},
	}

	got := c.FinalImagePath()
	if !strings.Contains(got, "disk-dev-") {
		t.Errorf("FinalImagePath = %q, should contain disk-dev-", got)
	}
	if !strings.HasSuffix(got, ".img") {
		t.Errorf("FinalImagePath = %q, should end with .img", got)
	}
}
