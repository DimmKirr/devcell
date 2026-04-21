package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

func ptrBool(b bool) *bool { return &b }

// buildTestArgv builds argv for a given binary+defaultFlags+userArgs using a
// controlled environment — no real docker, no real filesystem.
func buildTestArgv(binary string, defaultFlags, userArgs []string, envPairs ...string) []string {
	e := makeEnv(envPairs...)
	c := config.Load("/tmp/myproject", e)
	spec := runner.RunSpec{
		Config:       c,
		CellCfg:      cfg.CellConfig{},
		Binary:       binary,
		DefaultFlags: defaultFlags,
		UserArgs:     userArgs,
	}
	// No real fs (.env absent), no op in path
	return runner.BuildArgv(spec,
		runner.FSFunc(func(string) error { return os.ErrNotExist }),
		func(string) (string, error) { return "", os.ErrNotExist },
	)
}

func makeEnv(pairs ...string) func(string) string {
	m := map[string]string{
		"HOME": "/home/test",
		"USER": "test",
		"TERM": "xterm",
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

func trailingAfterImage(argv []string) []string {
	for i, a := range argv {
		if a == runner.UserImageTag() && i+1 < len(argv) {
			return argv[i+1:]
		}
	}
	return nil
}

// --- claude ---

func TestClaude_DefaultFlags(t *testing.T) {
	argv := buildTestArgv("claude", []string{"--dangerously-skip-permissions"}, nil)
	tail := trailingAfterImage(argv)
	if len(tail) == 0 || tail[0] != "claude" {
		t.Errorf("expected claude as binary, got: %v", tail)
	}
	if !hasArg(tail, "--dangerously-skip-permissions") {
		t.Errorf("missing --dangerously-skip-permissions, tail: %v", tail)
	}
}

func TestClaude_WithUserArgs(t *testing.T) {
	argv := buildTestArgv("claude", []string{"--dangerously-skip-permissions"}, []string{"--resume", "abc"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if !strings.HasSuffix(joined, "claude --dangerously-skip-permissions --resume abc") {
		t.Errorf("unexpected tail: %v", tail)
	}
}

// --- codex ---

func TestCodex_DefaultFlags(t *testing.T) {
	// No ollama: only --dangerously-bypass-approvals-and-sandbox; no --oss.
	argv := buildTestArgv("codex", []string{"--dangerously-bypass-approvals-and-sandbox"}, nil)
	tail := trailingAfterImage(argv)
	if tail[0] != "codex" {
		t.Errorf("expected codex binary, got: %v", tail)
	}
	if !hasArg(tail, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("missing --dangerously-bypass-approvals-and-sandbox in tail: %v", tail)
	}
	if hasArg(tail, "--oss") {
		t.Errorf("unexpected --oss without ollama in tail: %v", tail)
	}
}

func TestCodex_Resume(t *testing.T) {
	// codex resume: binary=codex, defaultFlags=nil, userArgs=["resume"]
	argv := buildTestArgv("codex", nil, []string{"resume"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if joined != "codex resume" {
		t.Errorf("expected 'codex resume', got: %q", joined)
	}
}

func TestCodex_ResumeWithArgs(t *testing.T) {
	argv := buildTestArgv("codex", nil, []string{"resume", "--conversation", "xyz"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if joined != "codex resume --conversation xyz" {
		t.Errorf("expected 'codex resume --conversation xyz', got: %q", joined)
	}
}

// --- opencode ---

func TestOpencode_NoDefaultFlags(t *testing.T) {
	argv := buildTestArgv("opencode", nil, []string{"."})
	tail := trailingAfterImage(argv)
	if tail[0] != "opencode" {
		t.Errorf("expected opencode binary, got: %v", tail)
	}
	if hasArg(tail, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("unexpected codex flag in opencode tail: %v", tail)
	}
	if tail[len(tail)-1] != "." {
		t.Errorf("expected '.' as last arg, got: %v", tail)
	}
}

func TestOpencode_DebugFlags(t *testing.T) {
	argv := buildTestArgv("opencode", []string{"--log-level", "DEBUG"}, []string{"."})
	tail := trailingAfterImage(argv)
	if !hasArg(tail, "--log-level") || !hasArg(tail, "DEBUG") {
		t.Errorf("expected --log-level DEBUG in tail: %v", tail)
	}
}

func TestOpencode_Resume(t *testing.T) {
	argv := buildTestArgv("opencode", nil, []string{"resume"})
	tail := trailingAfterImage(argv)
	joined := strings.Join(tail, " ")
	if joined != "opencode resume" {
		t.Errorf("expected 'opencode resume', got: %q", joined)
	}
}

// helper used across test files
func hasArg(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}
