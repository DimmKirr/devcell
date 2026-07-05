package ux_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

// CELL-262: PhaseRunner renders sequential cell-open phases as permanent ✓ rows.
// Each test mirrors the existing `plain()` ANSI-strip + captureStdout patterns
// used by banner_test.go and format_test.go.

// captureStdoutPhases is a local copy of the helper from format_test.go to
// keep this file self-contained — same shape.
func captureStdoutPhases(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// withPlainText forces plain-text mode for deterministic capture. Returns a
// teardown to restore.
func withPlainText(t *testing.T) func() {
	t.Helper()
	prev := ux.LogPlainText
	ux.LogPlainText = true
	return func() { ux.LogPlainText = prev }
}

func TestPhaseRunner_PhaseSuccessAppendsCheckmarkLine(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.Phase("Network", func() error { return nil })
	})
	stripped := plain(out)
	if !strings.Contains(stripped, "✓") {
		t.Errorf("missing ✓ in %q", stripped)
	}
	if !strings.Contains(stripped, "Network") {
		t.Errorf("missing phase name in %q", stripped)
	}
	if !strings.HasSuffix(stripped, "\n") {
		t.Errorf("Success row must end with newline; got %q", stripped)
	}
}

func TestPhaseRunner_PhaseFailureSurfacesError(t *testing.T) {
	defer withPlainText(t)()
	myErr := errors.New("boom")
	var returned error
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		returned = pr.Phase("Network", func() error { return myErr })
	})
	if !errors.Is(returned, myErr) {
		t.Errorf("Phase must return the closure's error verbatim; got %v", returned)
	}
	stripped := plain(out)
	if !strings.Contains(stripped, "Network") {
		t.Errorf("Fail row missing phase name: %q", stripped)
	}
	if !strings.Contains(stripped, "boom") {
		t.Errorf("Fail row missing error text: %q", stripped)
	}
}

func TestPhaseRunner_PhaseDetailedAppendsSuffix(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.PhaseDetailed("Loading secrets", func() (string, error) {
			return "7 resolved", nil
		})
	})
	stripped := plain(out)
	if !strings.Contains(stripped, "Loading secrets") {
		t.Errorf("missing phase name: %q", stripped)
	}
	if !strings.Contains(stripped, "— 7 resolved") {
		t.Errorf("missing em-dash + detail suffix: %q", stripped)
	}
}

func TestPhaseRunner_PhaseDetailedEmptyDetailOmitsSuffix(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.PhaseDetailed("Foo", func() (string, error) { return "", nil })
	})
	stripped := plain(out)
	if strings.Contains(stripped, "— ") {
		t.Errorf("empty detail must not produce trailing em-dash; got %q", stripped)
	}
	if !strings.Contains(stripped, "Foo") {
		t.Errorf("phase name missing: %q", stripped)
	}
}

func TestPhaseRunner_PhaseDetailedRunningSwapsLabel(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.PhaseDetailedRunning(
			"Loading secrets (please authorize 1Password)",
			"Loading secrets",
			func() (string, error) { return "7 resolved", nil },
		)
	})
	stripped := plain(out)
	if !strings.Contains(stripped, "Loading secrets — 7 resolved") {
		t.Errorf("final ✓ row must use finalName + detail: %q", stripped)
	}
	if strings.Contains(stripped, "✓ Loading secrets (please authorize") {
		t.Errorf("final ✓ row must NOT carry the running-label prompt: %q", stripped)
	}
}

func TestPhaseRunner_PhaseDetailedRunningFailureUsesFinalName(t *testing.T) {
	defer withPlainText(t)()
	myErr := errors.New("could not read \"Foo\" from 1Password")
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.PhaseDetailedRunning(
			"Loading secrets (please authorize 1Password)",
			"Loading secrets",
			func() (string, error) { return "", myErr },
		)
	})
	stripped := plain(out)
	if !strings.Contains(stripped, "Loading secrets — could not read") {
		t.Errorf("✗ row must use finalName + err: %q", stripped)
	}
	if strings.Contains(stripped, "✗ Loading secrets (please authorize") {
		t.Errorf("✗ row must NOT carry the running-label prompt: %q", stripped)
	}
}

func TestPhaseRunner_SealEmitsFinalRow(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		pr.Seal("Cell ready")
	})
	stripped := plain(out)
	if !strings.Contains(stripped, "✓") {
		t.Errorf("Seal must emit a ✓ row: %q", stripped)
	}
	if !strings.Contains(stripped, "Cell ready") {
		t.Errorf("Seal row missing name: %q", stripped)
	}
}

// TestPhaseRunner_PhasesAreSequential — both Phase rows survive (the second
// spinner doesn't clear the first row). Byte-position guard: "Foo" lands
// before "Bar" in the captured buffer.
func TestPhaseRunner_PhasesAreSequential(t *testing.T) {
	defer withPlainText(t)()
	out := captureStdoutPhases(func() {
		pr := &ux.PhaseRunner{}
		_ = pr.Phase("Foo", func() error { return nil })
		_ = pr.Phase("Bar", func() error { return nil })
	})
	stripped := plain(out)
	fooIdx := strings.Index(stripped, "Foo")
	barIdx := strings.Index(stripped, "Bar")
	if fooIdx < 0 || barIdx < 0 {
		t.Fatalf("both rows must be present; got %q", stripped)
	}
	if fooIdx >= barIdx {
		t.Errorf("Foo (idx=%d) must precede Bar (idx=%d); rows must not overwrite", fooIdx, barIdx)
	}
}
