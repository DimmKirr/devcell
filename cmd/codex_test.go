package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodex_OllamaFlag_InjectsFlags verifies that "cell codex --ollama --dry-run"
// passes --oss --local-provider ollama and CODEX_OSS_BASE_URL.
func TestCodex_OllamaFlag_InjectsFlags(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "codex", "--ollama", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "CODEX_OSS_BASE_URL=http://host.docker.internal:11434/v1") {
		t.Errorf("expected CODEX_OSS_BASE_URL in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "--oss") {
		t.Errorf("expected --oss in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "--local-provider ollama") {
		t.Errorf("expected --local-provider ollama in argv:\n%s", argv)
	}
}

// TestCodex_OllamaFlag_Stripped verifies --ollama is not forwarded to codex.
func TestCodex_OllamaFlag_Stripped(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "codex", "--ollama", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --ollama --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	for _, p := range strings.Fields(argv) {
		if p == "--ollama" {
			t.Errorf("--ollama should be stripped from argv, but found it:\n%s", argv)
		}
	}
}

// TestCodex_NoOllama_NoOSSFlags verifies that without ollama config,
// codex is started without --oss (uses cloud provider).
func TestCodex_NoOllama_NoOSSFlags(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, " --oss") {
		t.Errorf("--oss should not be passed without ollama mode:\n%s", argv)
	}
	if strings.Contains(argv, "--local-provider") {
		t.Errorf("--local-provider should not be passed without ollama mode:\n%s", argv)
	}
}

// TestCodex_ConfigUseOllama_InjectsFlags verifies that [llm] use_ollama=true
// in devcell.toml enables --oss --local-provider ollama.
func TestCodex_ConfigUseOllama_InjectsFlags(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
use_ollama = true
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "CODEX_OSS_BASE_URL=http://host.docker.internal:11434/v1") {
		t.Errorf("expected CODEX_OSS_BASE_URL from config:\n%s", argv)
	}
	if !strings.Contains(argv, "--oss") {
		t.Errorf("expected --oss from config:\n%s", argv)
	}
	if !strings.Contains(argv, "--local-provider ollama") {
		t.Errorf("expected --local-provider ollama from config:\n%s", argv)
	}
}

// TestCodex_ConfigUseOllama_WithModel verifies that llm.models.default is
// passed as --model when ollama is enabled.
func TestCodex_ConfigUseOllama_WithModel(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
use_ollama = true
[llm.models]
default = "qwen2.5-coder:32b"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "--model qwen2.5-coder:32b") {
		t.Errorf("expected --model qwen2.5-coder:32b in argv:\n%s", argv)
	}
}
