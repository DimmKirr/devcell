package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

// promptFileConfig mirrors sampleConfig() but points BaseDir at a temp dir so
// the writer has somewhere real to land, and carries a CellName so the
// per-cell namespacing is exercised.
func promptFileConfig(t *testing.T, cellName string) config.Config {
	t.Helper()
	return config.Config{
		AppName:  "devcell-85",
		BaseDir:  t.TempDir(),
		CellName: cellName,
		HostUser: "dmitry",
		HostHome: "/Users/dmitry",
	}
}

func TestWritePromptFile_WritesContentToHostPath(t *testing.T) {
	c := promptFileConfig(t, "main")

	if _, err := WritePromptFile(c, "additional-systemprompt.md", "hello prompt\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hostPath := filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md")
	got, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("expected file at %s: %v", hostPath, err)
	}
	if string(got) != "hello prompt\n" {
		t.Errorf("content mismatch:\n got %q\nwant %q", got, "hello prompt\n")
	}
}

func TestWritePromptFile_ReturnsContainerPathNotHostPath(t *testing.T) {
	c := promptFileConfig(t, "main")

	got, err := WritePromptFile(c, "additional-systemprompt.md", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "/devcell-85/.devcell/prompts/main/additional-systemprompt.md"
	if got != want {
		t.Errorf("container path:\n got %q\nwant %q", got, want)
	}
	// The host BaseDir must not leak into the path handed to the container.
	if filepath.IsAbs(c.BaseDir) && got != want {
		t.Errorf("returned path still carries the host base dir %q", c.BaseDir)
	}
}

// Two cells open on the same project must not clobber each other's file:
// ContainerContext embeds the cell name, so a shared path would let one cell
// boot with another's container context.
func TestWritePromptFile_NamespacedPerCell(t *testing.T) {
	base := t.TempDir()
	mk := func(cell string) config.Config {
		return config.Config{AppName: "devcell-85", BaseDir: base, CellName: cell}
	}

	pathA, err := WritePromptFile(mk("alpha"), "additional-systemprompt.md", "content-alpha")
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	pathB, err := WritePromptFile(mk("beta"), "additional-systemprompt.md", "content-beta")
	if err != nil {
		t.Fatalf("beta: %v", err)
	}

	if pathA == pathB {
		t.Fatalf("both cells resolved to the same container path %q", pathA)
	}

	for _, tc := range []struct{ cell, want string }{
		{"alpha", "content-alpha"},
		{"beta", "content-beta"},
	} {
		got, err := os.ReadFile(filepath.Join(base, ".devcell", "prompts", tc.cell, "additional-systemprompt.md"))
		if err != nil {
			t.Fatalf("%s: %v", tc.cell, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s clobbered: got %q want %q", tc.cell, got, tc.want)
		}
	}
}

// Re-running a cell must overwrite, not append or fail.
func TestWritePromptFile_OverwritesExisting(t *testing.T) {
	c := promptFileConfig(t, "main")

	if _, err := WritePromptFile(c, "additional-systemprompt.md", "first run"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := WritePromptFile(c, "additional-systemprompt.md", "second"); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("expected overwrite, got %q", got)
	}
}

// An unset cell name must still produce a usable path rather than a directory
// with an empty segment.
func TestWritePromptFile_EmptyCellNameFallsBack(t *testing.T) {
	c := promptFileConfig(t, "")

	got, err := WritePromptFile(c, "additional-systemprompt.md", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/devcell-85/.devcell/prompts/main/additional-systemprompt.md"
	if got != want {
		t.Errorf("empty cell name should fall back to %q, got %q", want, got)
	}
}

// WriteOverlayPrompt is the seam both surfaces use: assemble container
// context + resolved prompt, materialize it, hand back the container path.
func TestWriteOverlayPrompt_WritesAssembledContent(t *testing.T) {
	c := promptFileConfig(t, "main")

	got, err := WriteOverlayPrompt(c, cfg.CellConfig{}, ResolveOpts{AppendFlagInline: "be terse"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/devcell-85/.devcell/prompts/main/additional-systemprompt.md" {
		t.Errorf("container path = %q", got)
	}

	body, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(body)
	if !strings.Contains(content, "Docker container") {
		t.Error("overlay file missing container context")
	}
	if !strings.Contains(content, "be terse") {
		t.Error("overlay file missing resolved prompt")
	}
	if strings.Index(content, "be terse") <= strings.Index(content, "Docker container") {
		t.Error("container context must come before the resolved prompt")
	}
}

// With nothing configured the overlay still exists — container context is
// always present — so the flag is always emitted.
func TestWriteOverlayPrompt_ContextOnlyWhenNothingConfigured(t *testing.T) {
	c := promptFileConfig(t, "main")

	if _, err := WriteOverlayPrompt(c, cfg.CellConfig{}, ResolveOpts{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "Docker container") {
		t.Error("overlay file missing container context")
	}
}

func TestWriteOverlayPrompt_ResolverErrorPropagates(t *testing.T) {
	c := promptFileConfig(t, "main")

	_, err := WriteOverlayPrompt(c, cfg.CellConfig{}, ResolveOpts{
		AppendFlagInline: "a",
		AppendFlagFile:   "/nonexistent/b.md",
	})
	if err == nil {
		t.Fatal("expected mutually-exclusive resolver error to propagate")
	}
}
