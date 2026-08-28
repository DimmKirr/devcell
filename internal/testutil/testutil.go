// Package testutil provides shared test helpers for saving per-test artifacts
// to persistent output directories.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	repoRootOnce sync.Once
	repoRootPath string

	runTimestampOnce sync.Once
	runTimestamp     string

	resultsDirsMu sync.Mutex
	resultsDirs   = map[string]string{}
)

// RepoRoot returns the project root by walking up from the caller's source
// file to the nearest directory containing go.mod.
func RepoRoot() string {
	repoRootOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		dir := filepath.Dir(file)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				repoRootPath = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				panic(fmt.Sprintf("testutil: could not find project root (go.mod) from %s", file))
			}
			dir = parent
		}
	})
	return repoRootPath
}

// RunTimestamp returns a stable timestamp for the current test binary run.
// All tests in the same `go test` invocation share the same value.
func RunTimestamp() string {
	runTimestampOnce.Do(func() {
		runTimestamp = time.Now().Format("20060102T150405")
	})
	return runTimestamp
}

// ShortSHA returns the abbreviated commit hash of HEAD.
func ShortSHA() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = RepoRoot()
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("dev%s", time.Now().Format("150405"))
	}
	return strings.TrimSpace(string(out))
}

// TestResultsDir returns a persistent results directory for a test:
//
//	test/results/<timestamp>-<RootTestName>/<subtest/path>/
//
// All subtests of the same root test share one timestamped parent directory.
// The directory is created automatically.
//
// baseDirFn optionally overrides the base directory (the directory containing
// test/results/). When nil, RepoRoot() is used. Pass a custom function when
// the results path must be remapped (e.g. Docker host path translation).
func TestResultsDir(t *testing.T, baseDirFn func() string) string {
	t.Helper()

	root := t.Name()
	if i := strings.Index(root, "/"); i >= 0 {
		root = root[:i]
	}
	sub := strings.TrimPrefix(t.Name(), root)
	sub = strings.TrimPrefix(sub, "/")

	resultsDirsMu.Lock()
	defer resultsDirsMu.Unlock()

	baseDir, ok := resultsDirs[root]
	if !ok {
		parent := RepoRoot()
		if baseDirFn != nil {
			parent = baseDirFn()
		}
		stamp := time.Now().Format("20060102T150405")
		baseDir = filepath.Join(parent, "test", "results", stamp+"-"+root)
		resultsDirs[root] = baseDir
	}

	dir := baseDir
	if sub != "" {
		dir = filepath.Join(baseDir, sub)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("testutil: create results dir: %v", err)
	}
	t.Logf("test results: %s", dir)
	return dir
}
