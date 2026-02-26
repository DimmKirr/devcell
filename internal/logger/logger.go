package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/pterm/pterm"
)

var (
	defaultLogger *slog.Logger
	plainText     bool
)

func Initialize(logLevel string, plain bool) {
	plainText = plain

	var ptermLogLevel pterm.LogLevel
	switch strings.ToLower(logLevel) {
	case "debug":
		ptermLogLevel = pterm.LogLevelDebug
	case "warn", "warning":
		ptermLogLevel = pterm.LogLevelWarn
	case "error":
		ptermLogLevel = pterm.LogLevelError
	default:
		ptermLogLevel = pterm.LogLevelInfo
	}

	handler := pterm.NewSlogHandler(&pterm.DefaultLogger)
	pterm.DefaultLogger.Level = ptermLogLevel

	if !plain {
		applyTheme()
	}

	defaultLogger = slog.New(handler)
}

func applyTheme() {
	pterm.Info.Prefix = pterm.Prefix{
		Text:  "ℹ",
		Style: pterm.NewStyle(pterm.FgCyan, pterm.Bold),
	}
	pterm.Warning.Prefix = pterm.Prefix{
		Text:  "⚠",
		Style: pterm.NewStyle(pterm.FgYellow, pterm.Bold),
	}
	pterm.Success.Prefix = pterm.Prefix{
		Text:  "✔",
		Style: pterm.NewStyle(pterm.FgLightGreen, pterm.Bold),
	}
	pterm.Error.Prefix = pterm.Prefix{
		Text:  "⨯",
		Style: pterm.NewStyle(pterm.FgRed, pterm.Bold),
	}
}

func Info(msg string, keysAndValues ...interface{}) {
	defaultLogger.Info(msg, keysAndValues...)
}

func Debug(msg string, keysAndValues ...interface{}) {
	defaultLogger.Debug(msg, keysAndValues...)
}

func Warn(msg string, keysAndValues ...interface{}) {
	defaultLogger.Warn(msg, keysAndValues...)
}

func Error(msg string, keysAndValues ...interface{}) {
	defaultLogger.Error(msg, keysAndValues...)
}

func Fatal(msg string, keysAndValues ...interface{}) {
	defaultLogger.Error(msg, keysAndValues...)
	os.Exit(1)
}

func Println(msg string) {
	if !plainText {
		pterm.Println(fmt.Sprintf(" %s", msg))
	} else {
		defaultLogger.Info(msg)
	}
}

func init() {
	Initialize("info", false)
}
