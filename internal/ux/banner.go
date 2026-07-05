package ux

import (
	"strings"
)

// Banner renders the cell-open header — a single-line identity strip used at
// the top of every `cell <command>` invocation:
//
//	cell ▸ DIMM · devcell #304
//
// Color: "cell" + the chevron + "#bunk" use the muted palette so the eye lands
// on the cell name (brand orange + bold) and project (default fg). Two
// segments are suppressed when they carry no signal:
//   - cell name empty (single-pane default `main`)
//   - bunk "0" or "" — the no-multiplexer fallback; surfacing "#0" is noise.
func Banner(cell, project, bunk string) string {
	chevron := StyleMuted.Render("▸")
	parts := []string{StyleMuted.Render("cell"), chevron}
	if cell != "" {
		parts = append(parts,
			StyleInfo.Bold(true).Render(cell),
			StyleMuted.Render("·"),
		)
	}
	parts = append(parts, project)
	if bunk != "" && bunk != "0" {
		parts = append(parts, StyleMuted.Render("#"+bunk))
	}
	return strings.Join(parts, " ")
}

// KV renders an aligned key-value row for the cell-open detail block.
// keyWidth is the *key column width* — the value column starts at
// `keyWidth + 2` (2-space gap). Keys longer than keyWidth still get a minimum
// 2-space gap so nothing collides. Pure — no global state.
func KV(keyWidth int, key, value string) string {
	pad := keyWidth - len(key) + 2
	if pad < 2 {
		pad = 2
	}
	return key + strings.Repeat(" ", pad) + value
}
