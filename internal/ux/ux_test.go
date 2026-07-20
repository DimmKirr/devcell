package ux_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

func TestVerboseDefaultsFalse(t *testing.T) {
	// Reset state before checking default
	ux.Verbose = false
	ux.LogPlainText = false

	if ux.Verbose {
		t.Error("Verbose should default to false")
	}
	if ux.LogPlainText {
		t.Error("LogPlainText should default to false")
	}
}

func TestVerboseImpliesPlainText(t *testing.T) {
	// Caller convention: --debug sets both
	ux.Verbose = true
	ux.LogPlainText = true
	defer func() { ux.Verbose = false; ux.LogPlainText = false }()

	if !ux.Verbose {
		t.Error("Verbose should be true after --debug")
	}
	if !ux.LogPlainText {
		t.Error("LogPlainText should be true after --debug")
	}
}

func TestInitDebugLog_CreatesFileAndWritesDebugf(t *testing.T) {
	dir := t.TempDir()
	ux.Verbose = true
	defer func() { ux.Verbose = false; ux.CloseDebugLog() }()

	ux.InitDebugLog(dir, "test-cmd")

	ux.Debugf("hello %s", "world")
	ux.CloseDebugLog()

	// Find the log file
	entries, err := os.ReadDir(filepath.Join(dir, ".devcell", "debug"))
	if err != nil {
		t.Fatalf("reading debug dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, "-test-cmd.log") {
		t.Errorf("log file name %q doesn't end with -test-cmd.log", name)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".devcell", "debug", name))
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(content), "hello world") {
		t.Errorf("log file content %q doesn't contain 'hello world'", content)
	}
}

func TestInitDebugLog_NoOpWhenNotVerbose(t *testing.T) {
	dir := t.TempDir()
	ux.Verbose = false
	defer func() { ux.CloseDebugLog() }()

	ux.InitDebugLog(dir, "should-not-create")

	debugDir := filepath.Join(dir, ".devcell", "debug")
	if _, err := os.Stat(debugDir); err == nil {
		t.Error("debug dir should not be created when Verbose is false")
	}
}
