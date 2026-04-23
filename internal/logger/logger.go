package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	charmlog "github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

var (
	defaultLogger *slog.Logger
	plainText     bool
)

func Initialize(logLevel string, plain bool) {
	plainText = plain

	var level charmlog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = charmlog.DebugLevel
	case "warn", "warning":
		level = charmlog.WarnLevel
	case "error":
		level = charmlog.ErrorLevel
	default:
		level = charmlog.InfoLevel
	}

	opts := charmlog.Options{
		Level:           level,
		ReportTimestamp: plain,
	}
	logger := charmlog.NewWithOptions(os.Stderr, opts)
	if plain {
		logger.SetFormatter(charmlog.TextFormatter)
		logger.SetStyles(charmlog.DefaultStyles()) // reset to avoid nil
		// Force no-color output for plain/server mode
		logger.SetColorProfile(termenv.Ascii)
	}

	defaultLogger = slog.New(logger)
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
		fmt.Printf(" %s\n", msg)
	} else {
		defaultLogger.Info(msg)
	}
}

func init() {
	Initialize("info", false)
}
