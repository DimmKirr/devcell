package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	charmlog "github.com/charmbracelet/log"
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

	logger := charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level:           level,
		ReportTimestamp: false,
	})

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
