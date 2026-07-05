package ux_test

import (
	"regexp"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// CELL-48: Banner renders `cell ▸ <Cell> · <Project> #<id>` with styling.
// Tests assert the post-ANSI-strip form so the result is environment-agnostic.

func TestBanner_FormatsCellProjectId(t *testing.T) {
	got := plain(ux.Banner("DIMM", "devcell", "304"))
	want := "cell ▸ DIMM · devcell #304"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBanner_OmitsEmptyCellName(t *testing.T) {
	// When the cell-name resolves to the default "main", surfacing "main" adds
	// no signal — omit it so the header stays focused on project + id.
	got := plain(ux.Banner("", "devcell", "304"))
	want := "cell ▸ devcell #304"
	if got != want {
		t.Errorf("empty-cell: want %q, got %q", want, got)
	}
}

func TestBanner_OmitsZeroBunk(t *testing.T) {
	// bunk="0" is the no-multiplexer fallback. Suppress it so single-pane
	// users don't see a meaningless "#0".
	got := plain(ux.Banner("DIMM", "devcell", "0"))
	want := "cell ▸ DIMM · devcell"
	if got != want {
		t.Errorf("zero-bunk: want %q, got %q", want, got)
	}
}

// CELL-48: KV renders a left-padded key followed by two spaces and the value.
// keyWidth is the column width to pad to.

func TestKV_AlignsKeyToColumn(t *testing.T) {
	got := plain(ux.KV(8, "Cell", "DIMM"))
	want := "Cell      DIMM"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestKV_LongerKey_PadsToAtLeastOneSpace(t *testing.T) {
	got := plain(ux.KV(4, "Modules", "stack=go"))
	want := "Modules  stack=go"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}
