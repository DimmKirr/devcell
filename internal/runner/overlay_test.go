package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestRenderModulesOverlay_EmptyList covers the "no extra modules" case:
// the overlay must be a syntactically valid Nix expression that contributes
// nothing — so home-manager evaluates it as a no-op.
func TestRenderModulesOverlay_EmptyList(t *testing.T) {
	got := runner.RenderModulesOverlay(nil)
	if got == "" {
		t.Fatal("empty list must still render a parseable nix file, got empty string")
	}
	if !strings.Contains(got, "{") || !strings.Contains(got, "}") {
		t.Errorf("output is not a valid nix attrset:\n%s", got)
	}
}

// TestRenderModulesOverlay_SingleModule covers the smallest non-trivial case:
// one module name → one `devcell.modules.<name>.enable = true;` line.
func TestRenderModulesOverlay_SingleModule(t *testing.T) {
	got := runner.RenderModulesOverlay([]string{"electronics"})
	want := `devcell.modules."electronics".enable = true;`
	if !strings.Contains(got, want) {
		t.Errorf("output missing expected enable line %q:\n%s", want, got)
	}
}

// TestRenderModulesOverlay_MultipleModules — each module gets its own enable line.
func TestRenderModulesOverlay_MultipleModules(t *testing.T) {
	got := runner.RenderModulesOverlay([]string{"electronics", "publishing", "plex"})
	for _, m := range []string{"electronics", "publishing", "plex"} {
		want := `devcell.modules."` + m + `".enable = true;`
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestRenderModulesOverlay_QuotesNamesWithDashes — names like "yahoo-finance"
// and "project-management" must round-trip safely through Nix syntax.
func TestRenderModulesOverlay_QuotesNamesWithDashes(t *testing.T) {
	got := runner.RenderModulesOverlay([]string{"yahoo-finance", "project-management"})
	for _, m := range []string{"yahoo-finance", "project-management"} {
		want := `devcell.modules."` + m + `".enable = true;`
		if !strings.Contains(got, want) {
			t.Errorf("dashed name %q not rendered correctly:\n%s", m, got)
		}
	}
}

// TestRenderModulesOverlay_Deterministic — same input always produces byte-identical
// output (matters for the build cache's hash stability).
func TestRenderModulesOverlay_Deterministic(t *testing.T) {
	a := runner.RenderModulesOverlay([]string{"a", "b", "c"})
	b := runner.RenderModulesOverlay([]string{"a", "b", "c"})
	if a != b {
		t.Errorf("non-deterministic output:\n  first: %q\n  second: %q", a, b)
	}
}

// TestRenderModulesOverlay_HeaderMarksGenerated — the file should announce
// itself as generated so humans don't hand-edit it.
func TestRenderModulesOverlay_HeaderMarksGenerated(t *testing.T) {
	got := runner.RenderModulesOverlay([]string{"foo"})
	if !strings.Contains(strings.ToLower(got), "generated") {
		t.Errorf("missing 'generated' marker in header:\n%s", got)
	}
}

// TestWriteModulesOverlay writes a real file and verifies the content matches
// RenderModulesOverlay output. Catches I/O-layer mistakes.
func TestWriteModulesOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.nix")
	if err := runner.WriteModulesOverlay([]string{"electronics"}, path); err != nil {
		t.Fatalf("write error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(data) != runner.RenderModulesOverlay([]string{"electronics"}) {
		t.Errorf("file content != Render output\n got:\n%s\nwant:\n%s", string(data), runner.RenderModulesOverlay([]string{"electronics"}))
	}
}
