package ux

import (
	"fmt"
	"time"

	"github.com/pterm/pterm"
)

// LogPlainText disables spinners and uses plain logger output when true.
// Set before using any ux functions (e.g. when not a TTY or in CI).
var LogPlainText bool

// Verbose enables streaming of build output to stdout instead of suppressing it.
// Implies LogPlainText. Set by --debug.
var Verbose bool

// ProgressSpinner wraps pterm.SpinnerPrinter with a plain-text fallback.
type ProgressSpinner struct {
	spinner *pterm.SpinnerPrinter
	msg     string
}

// NewProgressSpinner creates and starts a spinner, or logs the message if in plain-text mode.
func NewProgressSpinner(message string) *ProgressSpinner {
	ps := &ProgressSpinner{msg: message}
	if !LogPlainText {
		s := pterm.DefaultSpinner
		s.Sequence = []string{" ⠋ ", " ⠙ ", " ⠹ ", " ⠸ ", " ⠼ ", " ⠴ ", " ⠦ ", " ⠧ ", " ⠇ ", " ⠏ "}
		s.Style = pterm.NewStyle(pterm.FgLightBlue)
		s.Delay = 80 * time.Millisecond
		s.ShowTimer = true
		ps.spinner, _ = s.Start(message)
	} else {
		pterm.Info.Println(message)
	}
	return ps
}

// UpdateText updates the spinner text or prints the message.
func (ps *ProgressSpinner) UpdateText(message string) *ProgressSpinner {
	if ps.spinner != nil {
		ps.spinner.UpdateText(message)
	} else {
		pterm.Info.Println(message)
	}
	return ps
}

// Success marks the spinner as successful.
func (ps *ProgressSpinner) Success(message string) *ProgressSpinner {
	if ps.spinner != nil {
		ps.spinner.Success(message)
	} else {
		pterm.Success.Println(message)
	}
	return ps
}

// Stop clears the spinner without leaving any output.
func (ps *ProgressSpinner) Stop() {
	if ps.spinner != nil && ps.spinner.IsActive {
		ps.spinner.Stop()
		// pterm Stop() may print a final frame; erase it and move cursor up.
		fmt.Print("\r\033[K\033[A\r\033[K")
	}
}

// Fail marks the spinner as failed.
func (ps *ProgressSpinner) Fail(message string) *ProgressSpinner {
	if ps.spinner != nil {
		ps.spinner.Fail(message)
	} else {
		pterm.Error.Println(message)
	}
	return ps
}

// GetConfirmation shows an interactive confirmation prompt (defaults to true).
func GetConfirmation(message string) (bool, error) {
	prefixed := fmt.Sprintf(" %s  %s", pterm.LightBlue("?"), message)
	return pterm.DefaultInteractiveConfirm.
		WithDefaultText(prefixed).
		WithDefaultValue(true).
		Show()
}

// GetSelection shows an interactive selection prompt and returns the chosen option.
func GetSelection(message string, options []string) (string, error) {
	prefixed := fmt.Sprintf(" %s  %s", pterm.LightBlue("?"), message)
	return pterm.DefaultInteractiveSelect.
		WithDefaultText(prefixed).
		WithOptions(options).
		Show()
}

// Println prints a styled line (or plain info when LogPlainText is set).
func Println(message string) {
	if !LogPlainText {
		pterm.Println(fmt.Sprintf(" %s", message))
	} else {
		pterm.Info.Println(message)
	}
}
