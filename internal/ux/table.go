package ux

import (
	"fmt"
	"os"
	"regexp"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ansiRe strips ANSI SGR escape sequences (colors, bold, etc.) from a string.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// SortKey identifies the column being sorted in the interactive table.
type SortKey string

const (
	SortRecommended SortKey = "r"
	SortSWE         SortKey = "s"
	SortSpeed       SortKey = "z"
	SortSize        SortKey = "p"
)

// SortKeyString converts a SortKey to the string value passed to RankModels sortBy.
func SortKeyString(key SortKey) string {
	switch key {
	case SortSWE:
		return "swe"
	case SortSpeed:
		return "speed"
	case SortSize:
		return "size"
	default:
		return "recommended"
	}
}

// InteractiveTable displays headers+rows in an interactive bubbles/table TUI.
// sortHandler is called when the user presses a sort key (r/s/z/p) and should
// return the re-sorted rows. Falls back to PrintTable for non-TTY or non-text mode.
func InteractiveTable(
	headers []string,
	rows [][]string,
	sortHandler func(key SortKey) [][]string,
) {
	if !isTTY() || OutputFormat != "text" {
		PrintTable(headers, rows)
		return
	}

	m := &tableModel{
		headers:     headers,
		rows:        rows,
		sortHandler: sortHandler,
		currentSort: SortRecommended,
	}
	m.rebuild()

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		PrintTable(headers, rows)
	}
}

type tableModel struct {
	t           table.Model
	headers     []string
	rows        [][]string
	sortHandler func(key SortKey) [][]string
	currentSort SortKey
	termHeight  int // set by tea.WindowSizeMsg; used to cap visible rows
}

var baseTableStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(colorBorder)

func (m *tableModel) rebuild() {
	cols := make([]table.Column, len(m.headers))
	for i, h := range m.headers {
		w := lipgloss.Width(h) + 2
		for _, row := range m.rows {
			if i < len(row) {
				if vw := lipgloss.Width(row[i]) + 2; vw > w {
					w = vw
				}
			}
		}
		if w > 40 {
			w = 40
		}
		cols[i] = table.Column{Title: h, Width: w}
	}

	// Strip ANSI codes before passing cells to bubbles/table.
	// Pre-styled cells (modGray.Render etc.) corrupt the table's internal
	// rendering — columns shift, content gets clipped. Plain text only here;
	// PrintTable uses the styled version for non-interactive output.
	trows := make([]table.Row, len(m.rows))
	for i, r := range m.rows {
		plain := make([]string, len(r))
		for j, cell := range r {
			plain[j] = stripANSI(cell)
		}
		trows[i] = table.Row(plain)
	}

	// Cap viewport height so the full view (blank+border+header+rows+border+help)
	// fits within the terminal. Total overhead = 7 lines (1 blank, 2 outer border,
	// 2 header+separator, 1 blank, 1 help). Default termHeight=24 until first
	// tea.WindowSizeMsg arrives.
	termH := m.termHeight
	if termH <= 0 {
		termH = 24
	}
	maxRows := termH - 7
	if maxRows < 3 {
		maxRows = 3
	}
	height := len(m.rows)
	if height > maxRows {
		height = maxRows
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(trows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#24292f", Dark: "#cdd9e5"})
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("#E85D26")).
		Bold(true)
	t.SetStyles(s)
	m.t = t
}

func (m *tableModel) Init() tea.Cmd { return nil }

func (m *tableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		m.rebuild()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r", "s", "z", "p":
			key := SortKey(msg.String())
			if m.sortHandler != nil {
				m.rows = m.sortHandler(key)
				m.currentSort = key
				m.rebuild()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.t, cmd = m.t.Update(msg)
	return m, cmd
}

func (m *tableModel) View() string {
	sortLabel := map[SortKey]string{
		SortRecommended: "Recommended",
		SortSWE:         "SWE-bench",
		SortSpeed:       "Speed",
		SortSize:        "Size",
	}[m.currentSort]

	help := StyleMuted.Render(fmt.Sprintf(
		"  Sort: [r]ecommended  [s]we-bench  [z]speed  [p]size  •  active: %s  •  [q]uit",
		sortLabel,
	))
	return "\n" + baseTableStyle.Render(m.t.View()) + "\n" + help + "\n"
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
