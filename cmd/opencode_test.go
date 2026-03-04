package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpencode_NoArgs_InjectsDot verifies that "cell opencode" (no user args)
// injects "." so opencode opens in the current directory.
func TestOpencode_NoArgs_InjectsDot(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	if !strings.HasSuffix(argv, " .") {
		t.Errorf("expected argv to end with '.', got: %s", argv)
	}
	// No codex flags should leak into opencode
	if strings.Contains(argv, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("codex flag leaked into opencode argv: %s", argv)
	}
}

// TestOpencode_WithArgs_NoDot verifies that "cell opencode --model foo" does NOT
// inject "." — user args are passed through as-is.
func TestOpencode_WithArgs_NoDot(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--dry-run", "--model", "foo")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	if !strings.HasSuffix(argv, "--model foo") {
		t.Errorf("expected argv to end with '--model foo', got: %s", argv)
	}
}

// TestOpencode_DebugOnly_InjectsDot verifies that "cell opencode --debug" still
// injects "." since --debug is a devcell flag, not an opencode arg.
// Also verifies --debug is translated to --log-level DEBUG for opencode.
func TestOpencode_DebugOnly_InjectsDot(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--debug", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --debug --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := strings.TrimSpace(string(out))
	if !strings.HasSuffix(argv, " .") {
		t.Errorf("expected argv to end with '.', got: %s", argv)
	}
	if strings.Contains(argv, " --debug") {
		t.Errorf("--debug should be stripped from opencode argv, got: %s", argv)
	}
	if !strings.Contains(argv, "--log-level DEBUG") {
		t.Errorf("expected --log-level DEBUG in argv, got: %s", argv)
	}
}

// TestOpencode_ConfigContentEnvInjected verifies OPENCODE_CONFIG_CONTENT is
// injected into the docker run argv with permission:"allow".
func TestOpencode_ConfigContentEnvInjected(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("OPENCODE_CONFIG_CONTENT not found in argv:\n%s", argv)
	}

	// Extract the JSON value from the -e OPENCODE_CONFIG_CONTENT=... arg
	jsonStr := extractEnvFromArgv(argv, "OPENCODE_CONFIG_CONTENT")
	if jsonStr == "" {
		t.Fatal("could not extract OPENCODE_CONFIG_CONTENT value")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, jsonStr)
	}
	if parsed["permission"] != "allow" {
		t.Errorf("expected permission: \"allow\", got: %v", parsed["permission"])
	}
}

// TestOpencode_ConfigContentWithOllama verifies [models] from devcell.toml
// is denormalized into OPENCODE_CONFIG_CONTENT.
func TestOpencode_ConfigContentWithOllama(t *testing.T) {
	home := scaffoldedHome(t)

	// Write devcell.toml with [models] section
	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[models]
default = "ollama/deepseek-r1:32b"
[models.providers.ollama]
models = ["deepseek-r1:32b", "qwen3:8b"]
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	jsonStr := extractEnvFromArgv(string(out), "OPENCODE_CONFIG_CONTENT")
	if jsonStr == "" {
		t.Fatal("could not extract OPENCODE_CONFIG_CONTENT value")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, jsonStr)
	}

	if parsed["model"] != "ollama/deepseek-r1:32b" {
		t.Errorf("expected model ollama/deepseek-r1:32b, got: %v", parsed["model"])
	}

	provider, ok := parsed["provider"].(map[string]interface{})
	if !ok {
		t.Fatalf("provider not a map: %v", parsed["provider"])
	}
	ollama, ok := provider["ollama"].(map[string]interface{})
	if !ok {
		t.Fatalf("ollama provider not found: %v", provider)
	}
	if ollama["npm"] != "@ai-sdk/openai-compatible" {
		t.Errorf("expected npm @ai-sdk/openai-compatible, got: %v", ollama["npm"])
	}
	opts := ollama["options"].(map[string]interface{})
	if opts["baseURL"] != "http://host.docker.internal:11434/v1" {
		t.Errorf("expected default ollama baseURL, got: %v", opts["baseURL"])
	}
	models := ollama["models"].(map[string]interface{})
	if _, ok := models["deepseek-r1:32b"]; !ok {
		t.Errorf("deepseek-r1:32b not in models: %v", models)
	}
	if _, ok := models["qwen3:8b"]; !ok {
		t.Errorf("qwen3:8b not in models: %v", models)
	}
}

// TestOpencode_ConfigContentNoModels verifies minimal config when no [models].
func TestOpencode_ConfigContentNoModels(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "opencode", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode --dry-run failed: %v\noutput: %s", err, out)
	}

	jsonStr := extractEnvFromArgv(string(out), "OPENCODE_CONFIG_CONTENT")
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := parsed["model"]; ok {
		t.Errorf("model should be omitted when empty, got: %v", parsed["model"])
	}
	provider := parsed["provider"].(map[string]interface{})
	if len(provider) != 0 {
		t.Errorf("expected empty provider map, got: %v", provider)
	}
}

// extractEnvFromArgv finds -e KEY=VALUE in a shell-joined argv string and
// returns VALUE. shellJoin wraps the whole "-e" arg in single quotes when it
// contains special chars, producing: 'KEY=VALUE'
func extractEnvFromArgv(argv, key string) string {
	// Look for 'KEY= (single-quoted form from shellJoin)
	quotedPrefix := "'" + key + "="
	idx := strings.Index(argv, quotedPrefix)
	if idx >= 0 {
		rest := argv[idx+len(quotedPrefix):]
		end := strings.Index(rest, "'")
		if end >= 0 {
			return rest[:end]
		}
	}
	// Fallback: unquoted KEY=VALUE
	prefix := key + "="
	idx = strings.Index(argv, prefix)
	if idx < 0 {
		return ""
	}
	rest := argv[idx+len(prefix):]
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		return rest[:end]
	}
	return rest
}
