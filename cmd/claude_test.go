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
	cmd.Dir = home
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
	cmd.Dir = home
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
	cmd.Dir = home
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
	cmd.Dir = home
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

// TestClaude_OllamaConfigModel_WithPrefix verifies that [llm.models] default = "ollama/model"
// injects ANTHROPIC_MODEL with the prefix stripped.
func TestClaude_OllamaConfigModel_WithPrefix(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
use_ollama = true

[llm.models]
default = "ollama/qwen3:30b"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "ANTHROPIC_MODEL=qwen3:30b") {
		t.Errorf("expected ANTHROPIC_MODEL=qwen3:30b (prefix stripped), got:\n%s", argv)
	}
	if strings.Contains(argv, "ANTHROPIC_MODEL=ollama/") {
		t.Errorf("ollama/ prefix should be stripped from ANTHROPIC_MODEL:\n%s", argv)
	}
}

// TestClaude_OllamaConfigModel_NoPrefix verifies that a model without "ollama/" prefix
// is passed through as-is.
func TestClaude_OllamaConfigModel_NoPrefix(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
use_ollama = true

[llm.models]
default = "qwen3:30b"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "ANTHROPIC_MODEL=qwen3:30b") {
		t.Errorf("expected ANTHROPIC_MODEL=qwen3:30b, got:\n%s", argv)
	}
}

// TestClaude_OllamaFlag_ConfigModel verifies that --ollama flag also picks up
// [llm.models] default from config (flag + config model should both work).
func TestClaude_OllamaFlag_ConfigModel(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm.models]
default = "ollama/deepseek-r1:32b"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "ANTHROPIC_MODEL=deepseek-r1:32b") {
		t.Errorf("expected ANTHROPIC_MODEL=deepseek-r1:32b when --ollama + config model:\n%s", argv)
	}
}

// TestClaude_OllamaNoModel_NoAnthropicModel verifies that without a configured model
// and no reachable ollama, ANTHROPIC_MODEL is not injected.
func TestClaude_OllamaNoModel_NoAnthropicModel(t *testing.T) {
	home := scaffoldedHome(t)

	// ollama not running in test env → auto-detect silently returns ""
	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "ANTHROPIC_MODEL=") {
		t.Errorf("ANTHROPIC_MODEL should not be set when no model configured and ollama unreachable:\n%s", argv)
	}
}

// TestClaude_OllamaWithUserArgs verifies that --ollama + user args work together.
func TestClaude_OllamaWithUserArgs(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--ollama", "--dry-run", "--resume")
	cmd.Dir = home
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
