package op_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/op"
)

// CELL-42: ShouldResolve is the pure gate that decides whether to invoke
// `op item get` for the configured documents. Skip when there's nothing to
// resolve, when the user opted out via flag, or via env var.

func TestShouldResolve_NoDocs_Skips(t *testing.T) {
	if op.ShouldResolve(false, "", nil) {
		t.Error("empty docs must skip resolution")
	}
}

func TestShouldResolve_FlagSet_Skips(t *testing.T) {
	if op.ShouldResolve(true, "", []string{"some-doc"}) {
		t.Error("--no-1password flag must skip resolution even with docs configured")
	}
}

func TestShouldResolve_EnvOne_Skips(t *testing.T) {
	if op.ShouldResolve(false, "1", []string{"some-doc"}) {
		t.Error("DEVCELL_NO_1PASSWORD=1 must skip resolution")
	}
}

func TestShouldResolve_EnvTrue_Skips(t *testing.T) {
	if op.ShouldResolve(false, "true", []string{"some-doc"}) {
		t.Error("DEVCELL_NO_1PASSWORD=true must skip resolution")
	}
}

func TestShouldResolve_EnvZero_Resolves(t *testing.T) {
	if !op.ShouldResolve(false, "0", []string{"some-doc"}) {
		t.Error("DEVCELL_NO_1PASSWORD=0 must NOT skip — user explicitly turned it off")
	}
}

func TestShouldResolve_DefaultsToResolve(t *testing.T) {
	if !op.ShouldResolve(false, "", []string{"some-doc"}) {
		t.Error("docs present + flag/env unset must resolve")
	}
}
