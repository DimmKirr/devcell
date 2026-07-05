package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-43: stacks are deprecated (see CELL-6/337). The "stack=X" qualifier
// in build progress should only appear when the user opted into a stack
// explicitly (TOML/--stack/env). Otherwise we leak the resolved default and
// imply opt-in where there wasn't any.

func TestBuildLabel_ImplicitStackOmitsQualifier(t *testing.T) {
	got := runner.BuildLabel("Building thin image", "base", false)
	want := "Building thin image"
	if got != want {
		t.Errorf("implicit stack: want %q, got %q", want, got)
	}
}

func TestBuildLabel_ExplicitStackIncludesQualifier(t *testing.T) {
	got := runner.BuildLabel("Building thin image", "go", true)
	want := "Building thin image (stack=go)"
	if got != want {
		t.Errorf("explicit stack: want %q, got %q", want, got)
	}
}
