// Package testutil provides shared test helpers for saving per-test artifacts
// to persistent output directories for later LLM review.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	runTimestamp     string
	runTimestampOnce sync.Once
)

// RunTimestamp returns a stable timestamp for the current test run.
// All tests in the same `go test` invocation share the same timestamp.
func RunTimestamp() string {
	runTimestampOnce.Do(func() {
		runTimestamp = time.Now().Format("20060102-150405")
	})
	return runTimestamp
}

// ArtifactDir returns a persistent directory for saving test artifacts:
//
//	test/testdata/<run-timestamp>/<TestName>/
//
// The directory is created automatically. Files written here survive after
// the test finishes, so they can be reviewed by humans or LLMs.
// rootDir should be the path to the repo root (e.g. "../.." from internal/scaffold).
func ArtifactDir(t *testing.T, rootDir string) string {
	t.Helper()
	// Sanitize test name: slashes from subtests become dashes
	name := strings.ReplaceAll(t.Name(), "/", "-")
	dir := filepath.Join(rootDir, "test", "testdata", RunTimestamp(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	return dir
}

// SaveArtifact writes content to a named file in the test's artifact directory.
func SaveArtifact(t *testing.T, dir, filename string, content []byte) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("save artifact %s: %v", filename, err)
	}
	t.Logf("artifact: %s", path)
}
