package ux

import "fmt"

// FormatSecretsPhase renders the post-resolution *detail* attached to the
// phase's final ✓ row (label is owned by the PhaseRunner — passing a prefixed
// label here doubles up as "Loaded secrets — Loaded secrets — 7 resolved").
// CELL-261.
//
// Elapsed time is NOT included here — the PhaseRunner appends its own elapsed
// marker, so embedding one would double-stamp the row.
func FormatSecretsPhase(count, failed int) string {
	if failed > 0 {
		return fmt.Sprintf("%d resolved, %d failed", count, failed)
	}
	return fmt.Sprintf("%d resolved", count)
}
