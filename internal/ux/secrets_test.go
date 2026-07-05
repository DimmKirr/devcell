package ux_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

// CELL-261: FormatSecretsPhase renders the permanent post-resolution line
// shown below the cell-open banner when 1Password documents are configured.
// Tests assert the post-ANSI-strip form so the result is environment-agnostic
// — same convention as banner_test.go.
//
// Elapsed time is NOT embedded — ProgressSpinner.Success appends its own
// elapsed marker. These tests pin that constraint so a future refactor that
// re-adds it here gets caught.

func TestFormatSecretsPhase_AllResolved(t *testing.T) {
	got := plain(ux.FormatSecretsPhase(7, 0))
	want := "7 resolved"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestFormatSecretsPhase_WithFailures(t *testing.T) {
	got := plain(ux.FormatSecretsPhase(5, 2))
	want := "5 resolved, 2 failed"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// Caller must not embed the phase label — PhaseRunner prepends finalName.
// If this leaks back in, the row reads "Loaded secrets — Loaded secrets — N".
func TestFormatSecretsPhase_NoLabelPrefix(t *testing.T) {
	for _, in := range []string{
		ux.FormatSecretsPhase(7, 0),
		ux.FormatSecretsPhase(5, 2),
	} {
		got := plain(in)
		for _, banned := range []string{"Loading", "Loaded", "secrets"} {
			if strings.Contains(got, banned) {
				t.Errorf("detail must not include label fragment %q; got %q", banned, got)
			}
		}
	}
}

func TestFormatSecretsPhase_ZeroFailedOmitsClause(t *testing.T) {
	got := plain(ux.FormatSecretsPhase(3, 0))
	if strings.Contains(got, "failed") {
		t.Errorf("zero failures must not show ', N failed' clause; got %q", got)
	}
}

// No elapsed substring (no "ms", "s", "µs") — Success() owns elapsed.
func TestFormatSecretsPhase_OmitsElapsed(t *testing.T) {
	got := plain(ux.FormatSecretsPhase(3, 0))
	for _, suffix := range []string{"ms", "µs", "ns"} {
		if strings.Contains(got, suffix) {
			t.Errorf("must not embed elapsed time (Success() appends it); got %q contains %q", got, suffix)
		}
	}
}
