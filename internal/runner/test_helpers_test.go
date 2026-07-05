package runner_test

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

// captureStdout redirects os.Stdout for the duration of fn and returns the
// captured output. Used by tests that assert on what ProgressSpinner /
// ux.* primitives write to stdout. Adapted from internal/ux/format_test.go.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
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

// withPlainText forces ux.LogPlainText=true so ProgressSpinner's async
// ticker is disabled and stdout deterministically reflects the permanent
// Success/Fail lines. Returns a teardown that restores the prior value.
func withPlainText(t *testing.T) func() {
	t.Helper()
	prev := ux.LogPlainText
	ux.LogPlainText = true
	return func() { ux.LogPlainText = prev }
}
