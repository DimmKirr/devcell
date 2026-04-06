package ux

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// LogPlainText disables spinners and uses plain logger output when true.
// Set before using any ux functions (e.g. when not a TTY or in CI).
var LogPlainText bool

// Verbose enables streaming of build output to stdout instead of suppressing it.
// Implies LogPlainText. Set by --debug.
var Verbose bool

// Color palette — adaptive hex pairs for light/dark terminal themes.
// Info/spinner: brand orange. Success: brand lime (softened). Error/Warning: Primer (semantic clarity).
var (
	colorInfo    = lipgloss.AdaptiveColor{Light: "#C05420", Dark: "#E85D26"} // brand orange
	colorSuccess = lipgloss.AdaptiveColor{Light: "#5A7A00", Dark: "#A8D400"} // brand lime (tamed)
	colorError   = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"} // Primer red
	colorWarning = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"} // Primer amber
	colorAccent  = lipgloss.AdaptiveColor{Light: "#5A7A00", Dark: "#C8FF00"} // brand lime full
	colorDebug   = lipgloss.Color("#636e7b")
	colorMuted   = lipgloss.Color("#8b949e")
	colorBorder  = lipgloss.Color("#30363d")
)

// Styles — exported so commands can reuse instead of defining their own.
var (
	StyleInfo    = lipgloss.NewStyle().Foreground(colorInfo)
	StyleSuccess = lipgloss.NewStyle().Foreground(colorSuccess)
	StyleError   = lipgloss.NewStyle().Foreground(colorError)
	StyleWarning = lipgloss.NewStyle().Foreground(colorWarning)
	StyleDebug   = lipgloss.NewStyle().Foreground(colorDebug)
	StyleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	StyleBold    = lipgloss.NewStyle().Bold(true)
	StyleSection = lipgloss.NewStyle().Bold(true)
	StyleAccent  = lipgloss.NewStyle().Foreground(colorAccent)

	// TableBorder is the default style for lipgloss/table borders.
	TableBorder = lipgloss.NewStyle().Foreground(colorBorder)
)

// prefix renders a fixed-width (6 char) prefix for aligned message columns.
func prefix(style lipgloss.Style, text string) string {
	return style.Width(6).Render(text)
}

// ProgressSpinner displays an animated spinner with a message.
// In plain-text mode it falls back to simple log lines.
type ProgressSpinner struct {
	msg    string
	start  time.Time
	mu     sync.Mutex
	done   chan struct{}
	active bool
}

// NewProgressSpinner creates and starts a spinner, or logs the message if in plain-text mode.
func NewProgressSpinner(message string) *ProgressSpinner {
	ps := &ProgressSpinner{msg: message, start: time.Now()}
	if !LogPlainText {
		ps.done = make(chan struct{})
		ps.active = true
		go ps.run()
	} else {
		fmt.Printf(" %s %s\n", prefix(StyleInfo, "→"), message)
	}
	return ps
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (ps *ProgressSpinner) run() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-ps.done:
			fmt.Print("\r\033[K") // clear the spinner line
			return
		case <-ticker.C:
			ps.mu.Lock()
			msg := ps.msg
			elapsed := time.Since(ps.start).Round(time.Millisecond)
			ps.mu.Unlock()
			frame := spinnerFrames[i%len(spinnerFrames)]
			fmt.Printf("\r\033[K %s %s %s",
				StyleInfo.Render(frame),
				msg,
				StyleMuted.Render(elapsed.String()),
			)
			i++
		}
	}
}

// UpdateText updates the spinner text or prints the message.
func (ps *ProgressSpinner) UpdateText(message string) *ProgressSpinner {
	ps.mu.Lock()
	ps.msg = message
	ps.mu.Unlock()
	if !ps.active {
		fmt.Printf(" %s %s\n", prefix(StyleInfo, "→"), message)
	}
	return ps
}

// Success stops the spinner and prints a success message.
func (ps *ProgressSpinner) Success(message string) *ProgressSpinner {
	ps.stop()
	elapsed := time.Since(ps.start).Round(time.Millisecond)
	fmt.Printf(" %s %s %s\n", prefix(StyleSuccess, "✓"), message, StyleMuted.Render(elapsed.String()))
	return ps
}

// Stop clears the spinner without leaving any output.
func (ps *ProgressSpinner) Stop() {
	ps.stop()
}

func (ps *ProgressSpinner) stop() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.active {
		close(ps.done)
		ps.active = false
		// Small sleep to let the goroutine clear the line before we return.
		time.Sleep(10 * time.Millisecond)
	}
}

// Fail stops the spinner and prints a failure message.
func (ps *ProgressSpinner) Fail(message string) *ProgressSpinner {
	ps.stop()
	fmt.Printf(" %s %s\n", prefix(StyleError, "✗"), message)
	return ps
}

// ErrUserAborted is returned when the user presses Esc during a prompt.
var ErrUserAborted = huh.ErrUserAborted

// GetConfirmation shows an interactive confirmation prompt (defaults to true).
func GetConfirmation(message string) (bool, error) {
	var confirmed bool
	field := huh.NewConfirm().
		Title(message).
		Affirmative("Yes").
		Negative("No").
		Value(&confirmed)
	var err error
	if LogPlainText {
		err = field.RunAccessible(os.Stdout, os.Stdin)
	} else {
		err = field.Run()
	}
	if err != nil {
		return false, err
	}
	return confirmed, nil
}

// SelectOption pairs a display label with a value for typed selection.
type SelectOption struct {
	Label string
	Value string
}

// GetSelection shows an interactive selection prompt and returns the chosen option.
func GetSelection(message string, options []string) (string, error) {
	opts := make([]SelectOption, len(options))
	for i, o := range options {
		opts[i] = SelectOption{Label: o, Value: o}
	}
	return GetSelectionKV(message, opts)
}

// GetSelectionKV shows an interactive selection with separate display labels and values.
// Returns the Value of the selected option.
func GetSelectionKV(message string, options []SelectOption) (string, error) {
	var selected string
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o.Label, o.Value)
	}
	field := huh.NewSelect[string]().
		Title(message).
		Options(opts...).
		Value(&selected).
		WithHeight(len(options) + 2)
	if LogPlainText {
		err := field.RunAccessible(os.Stdout, os.Stdin)
		if err != nil {
			return "", err
		}
		return selected, nil
	}
	km := huh.NewDefaultKeyMap()
	km.Quit.SetKeys("ctrl+c", "esc")
	err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithKeyMap(km).
		Run()
	if err != nil {
		return "", err
	}
	return selected, nil
}

// GetMultiSelection shows an interactive multi-select (checkbox) prompt and
// returns all selected options. defaultOptions are pre-checked.
// Returns huh.ErrUserAborted if the user presses Esc or Ctrl+C.
func GetMultiSelection(message string, options []string, defaultOptions []string) ([]string, error) {
	selected := make([]string, len(defaultOptions))
	copy(selected, defaultOptions)

	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o)
	}
	field := huh.NewMultiSelect[string]().
		Title(message).
		Options(opts...).
		Value(&selected).
		WithHeight(len(options) + 2)
	if LogPlainText {
		err := field.RunAccessible(os.Stdout, os.Stdin)
		if err != nil {
			return nil, err
		}
		return selected, nil
	}
	// Build form manually so we can add Esc to the Quit binding.
	km := huh.NewDefaultKeyMap()
	km.Quit.SetKeys("ctrl+c", "esc")
	err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithKeyMap(km).
		Run()
	if err != nil {
		return nil, err
	}
	return selected, nil
}

// Debugf prints a formatted debug message when Verbose (--debug) is enabled.
func Debugf(format string, a ...any) {
	if Verbose {
		fmt.Printf(" %s %s\n", prefix(StyleDebug, "DBG"), fmt.Sprintf(format, a...))
	}
}

// Println prints a styled line (or plain info when LogPlainText is set).
func Println(message string) {
	if !LogPlainText {
		fmt.Printf(" %s\n", message)
	} else {
		fmt.Printf(" %s %s\n", prefix(StyleInfo, "→"), message)
	}
}

// Info prints an info-styled message.
func Info(message string) {
	fmt.Printf(" %s %s\n", prefix(StyleInfo, "→"), message)
}

// Warn prints a warning-styled message.
func Warn(message string) {
	fmt.Printf(" %s %s\n", prefix(StyleWarning, "WARN"), message)
}

// SuccessMsg prints a success-styled message (standalone, not spinner).
func SuccessMsg(message string) {
	fmt.Printf(" %s %s\n", prefix(StyleSuccess, "✓"), message)
}
