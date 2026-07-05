package ux_test

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DimmKirr/devcell/internal/ux"
)

// Tiny ANSI processor for "what would a real terminal show, given these
// bytes?" — used by the spinner stop-race test below. Implements only the
// escape sequences ProgressSpinner emits: \r, \n, \033[K (EL), \033[s
// (DECSC save), \033[u (DECRC restore), \033[J (ED), and printable runes
// (UTF-8). SGR (`\033[...m`) is recognised and dropped because we only
// care about visible characters.

type virtScreen struct {
	rows    [][]rune
	row     int
	col     int
	saveRow int
	saveCol int
	hasSave bool
}

func (s *virtScreen) ensureRow(r int) {
	for len(s.rows) <= r {
		s.rows = append(s.rows, nil)
	}
}

func (s *virtScreen) writeRune(r rune) {
	s.ensureRow(s.row)
	for len(s.rows[s.row]) <= s.col {
		s.rows[s.row] = append(s.rows[s.row], ' ')
	}
	s.rows[s.row][s.col] = r
	s.col++
}

// renderANSI replays a byte stream onto a virtual screen and returns the
// final visible text (rows joined with \n, trailing spaces trimmed).
// Faithful enough for the spinner's specific escape vocabulary.
func renderANSI(data []byte) string {
	s := &virtScreen{}
	i := 0
	for i < len(data) {
		b := data[i]
		if b == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			// CSI sequence: \033[<params><cmd>
			j := i + 2
			for j < len(data) && (data[j] >= '0' && data[j] <= '9' || data[j] == ';') {
				j++
			}
			if j >= len(data) {
				break
			}
			cmd := data[j]
			params := string(data[i+2 : j])
			switch cmd {
			case 'K': // EL - Erase in Line
				mode := 0
				if params != "" {
					mode, _ = strconv.Atoi(params)
				}
				s.ensureRow(s.row)
				switch mode {
				case 0: // cursor to EOL
					if s.col < len(s.rows[s.row]) {
						s.rows[s.row] = s.rows[s.row][:s.col]
					}
				case 1: // BOL to cursor
					for k := 0; k < s.col && k < len(s.rows[s.row]); k++ {
						s.rows[s.row][k] = ' '
					}
				case 2: // entire line
					s.rows[s.row] = nil
				}
			case 's': // DECSC - save cursor
				s.saveRow = s.row
				s.saveCol = s.col
				s.hasSave = true
			case 'u': // DECRC - restore cursor
				if s.hasSave {
					s.row = s.saveRow
					s.col = s.saveCol
				} else {
					// macOS Terminal.app / iTerm2 / many xterm-family
					// terminals interpret restore-with-no-prior-save as
					// "jump to home (1,1)". xterm itself treats it as
					// a no-op. We model the more conservative (buggier
					// for our code) behavior so the test catches the
					// fast-phase regression.
					s.row = 0
					s.col = 0
				}
			case 'J': // ED - Erase in Display
				mode := 0
				if params != "" {
					mode, _ = strconv.Atoi(params)
				}
				if mode == 0 {
					s.ensureRow(s.row)
					if s.col < len(s.rows[s.row]) {
						s.rows[s.row] = s.rows[s.row][:s.col]
					}
					if s.row+1 < len(s.rows) {
						s.rows = s.rows[:s.row+1]
					}
				} else if mode == 2 {
					s.rows = nil
					s.row = 0
					s.col = 0
				}
			case 'F': // CPL - Cursor Previous Line
				n := 1
				if params != "" {
					n, _ = strconv.Atoi(params)
				}
				s.row -= n
				if s.row < 0 {
					s.row = 0
				}
				s.col = 0
			case 'A': // CUU - Cursor Up
				n := 1
				if params != "" {
					n, _ = strconv.Atoi(params)
				}
				s.row -= n
				if s.row < 0 {
					s.row = 0
				}
			case 'm':
				// SGR (colour/style) — dropped; we only assert on visible text
			}
			i = j + 1
			continue
		}
		switch b {
		case '\n':
			s.row++
			s.col = 0
			i++
		case '\r':
			s.col = 0
			i++
		default:
			if b >= 0x20 {
				r, size := utf8.DecodeRune(data[i:])
				s.writeRune(r)
				i += size
			} else {
				i++
			}
		}
	}
	var lines []string
	for _, row := range s.rows {
		lines = append(lines, strings.TrimRight(string(row), " "))
	}
	return strings.Join(lines, "\n")
}

// captureStdoutBytes mirrors the existing captureStdout helper but returns
// raw bytes so renderANSI can see escape sequences. Restores os.Stdout.
func captureStdoutBytes(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return data
}

// TestProgressSpinner_FastPhaseNoCursorJump verifies the spinner doesn't
// emit a stray cursor-restore (`\x1b[u`) when nothing was ever drawn —
// the "fast phase" case where work completes before the 80ms tick.
//
// Bug observed: after wiring DECSC/DECRC into the spinner, host phases
// like Network / Orphan check (which run in <50ms) emitted `\x1b[u` with
// no prior save. Behaviour of restore-with-no-save is terminal-specific:
//   - xterm: no-op
//   - Terminal.app / iTerm2: cursor moves to home (1,1)
// On the affected terminals, the first phase's `\x1b[u\r\x1b[K` jumped
// to row 1 and cleared it; subsequent phases then wrote each row at the
// same position, leaving only the LAST phase visible.
//
// Fix: only emit the restore-and-clear sequence if we ticked at least
// once. Fast phases produce no output at all from the spinner; Success
// just prints the permanent row directly at the current cursor.
func TestProgressSpinner_FastPhaseEmitsNothingFromRunner(t *testing.T) {
	prev := ux.LogPlainText
	ux.LogPlainText = false
	defer func() { ux.LogPlainText = prev }()

	data := captureStdoutBytes(t, func() {
		// Banner-like content; the test asserts this survives unscathed.
		fmt.Print(" cell ▸ DIMM · devcell\n")
		// Fast phase: spinner created, Stop called before any tick fires.
		sp := ux.NewProgressSpinner("Network")
		// No work, no sleep — immediate Stop, no tick possible.
		sp.Stop()
		// Then a normal Success-style row writes (we simulate what
		// Success would emit). The visible row count should be 2: the
		// banner and this success line, with no jumps or blanks.
		fmt.Print(" ✓ Network 0ms\n")
	})

	visible := renderANSI(data)
	if !strings.Contains(visible, "cell ▸ DIMM · devcell") {
		t.Errorf("banner must survive a fast-phase spinner Stop; rendered buffer:\n%s", visible)
	}
	if !strings.Contains(visible, "Network") {
		t.Errorf("✓ Network row must be visible after Success; rendered buffer:\n%s", visible)
	}
	// The buffer's second line (where the ✓ row lands) MUST be the network
	// row, not whatever the banner had (proves no cursor-home jump).
	lines := strings.Split(visible, "\n")
	if len(lines) < 2 || !strings.Contains(lines[1], "Network") {
		t.Errorf("expected line 2 = ✓ Network, got %d lines:\n%s", len(lines), visible)
	}
}

// TestProgressSpinner_StopClearsFrameEvenAfterExternalOutput is the RED
// test for the ghost-row bug observed in CELL-264 cell-shell smoke runs:
//
//	✓ Starting GUI 30.778s
//	⠋ GUI ready 82ms%      ← ghost (no newline; cursor never returned to clear it)
//	✓ GUI ready 30.978s
//	[shell prompt]
//
// Race reproduced here without a real shell:
//  1. spinner ticks (writes a frame to row N)
//  2. simulate external output that adds a newline (cursor → row N+1)
//  3. spinner Stop → run() writes `\r\033[K`, but cursor is at row N+1,
//     so the clear hits the wrong row and the frame at row N survives
//
// FIX (sibling task): the spinner saves cursor with `\033[s` (DECSC) on
// each tick, and Stop restores with `\033[u` (DECRC) before clearing.
// Then no matter where external output moved the cursor, the clear
// targets the spinner's actual row.
func TestProgressSpinner_StopClearsFrameEvenAfterExternalOutput(t *testing.T) {
	// Need animation enabled — LogPlainText=false.
	prev := ux.LogPlainText
	ux.LogPlainText = false
	defer func() { ux.LogPlainText = prev }()

	data := captureStdoutBytes(t, func() {
		sp := ux.NewProgressSpinner("Loading mise tools")
		// Wait for at least one tick (spinner ticks every 80ms).
		time.Sleep(120 * time.Millisecond)
		// Simulate external output that moves the cursor — exactly what
		// zsh's prompt draw does between our last tick and our Stop call.
		fmt.Print("\n$ shell-prompt\n")
		sp.Stop()
	})

	visible := renderANSI(data)
	if strings.Contains(visible, "Loading mise tools") {
		t.Errorf("GHOST ROW: spinner frame survived Stop() after external cursor move.\n"+
			"--- rendered visible buffer ---\n%s\n--- end ---\n"+
			"Expected: 'Loading mise tools' nowhere in visible output.",
			visible)
	}
}
