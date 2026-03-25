package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaude_OllamaFlag_InjectsEnv verifies that "cell claude --ollama --dry-run"
// injects ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, and
// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC into docker argv.
func TestClaude_OllamaFlag_InjectsEnv(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "ANTHROPIC_BASE_URL=http://host.docker.internal:11434") {
		t.Errorf("expected ANTHROPIC_BASE_URL in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "ANTHROPIC_AUTH_TOKEN=ollama") {
		t.Errorf("expected ANTHROPIC_AUTH_TOKEN=ollama in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1") {
		t.Errorf("expected CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 in argv:\n%s", argv)
	}
}

// TestClaude_OllamaFlag_Stripped verifies --ollama is NOT forwarded to claude binary.
func TestClaude_OllamaFlag_Stripped(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	// After the image tag, --ollama should not appear
	argv := strings.TrimSpace(string(out))
	// Split and find args after image tag
	parts := strings.Fields(argv)
	for _, p := range parts {
		if p == "--ollama" {
			t.Errorf("--ollama should be stripped from argv, but found it:\n%s", argv)
		}
	}
}

// TestClaude_NoOllama_NoEnv verifies that without --ollama flag or config,
// no ANTHROPIC_BASE_URL is injected.
func TestClaude_NoOllama_NoEnv(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "ANTHROPIC_BASE_URL") {
		t.Errorf("ANTHROPIC_BASE_URL should not be set without --ollama:\n%s", argv)
	}
	if strings.Contains(argv, "ANTHROPIC_AUTH_TOKEN") {
		t.Errorf("ANTHROPIC_AUTH_TOKEN should not be set without --ollama:\n%s", argv)
	}
}

// TestClaude_ConfigUseOllama_InjectsEnv verifies that [llm] use_ollama=true
// in devcell.toml injects the ollama env vars.
func TestClaude_ConfigUseOllama_InjectsEnv(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
use_ollama = true
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "ANTHROPIC_BASE_URL=http://host.docker.internal:11434") {
		t.Errorf("expected ANTHROPIC_BASE_URL from config:\n%s", argv)
	}
	if !strings.Contains(argv, "ANTHROPIC_AUTH_TOKEN=ollama") {
		t.Errorf("expected ANTHROPIC_AUTH_TOKEN=ollama from config:\n%s", argv)
	}
}

// TestClaude_OllamaWithUserArgs verifies that --ollama + user args work together.
func TestClaude_OllamaWithUserArgs(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run", "--resume")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --ollama --dry-run --resume failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	// --resume should be forwarded
	if !strings.Contains(argv, "--resume") {
		t.Errorf("expected --resume in argv:\n%s", argv)
	}
	// --ollama should NOT be forwarded
	fields := strings.Fields(argv)
	for _, f := range fields {
		if f == "--ollama" {
			t.Errorf("--ollama leaked into argv:\n%s", argv)
		}
	}
	// ollama env should be present
	if !strings.Contains(argv, "ANTHROPIC_BASE_URL") {
		t.Errorf("expected ANTHROPIC_BASE_URL in argv:\n%s", argv)
	}
}
