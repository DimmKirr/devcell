package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodex_DeveloperInstructions_ContainerContext verifies that cell codex
// injects -c developer_instructions=... containing container context markers.
func TestCodex_DeveloperInstructions_ContainerContext(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "-c") {
		t.Errorf("expected -c flag in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "developer_instructions=") {
		t.Errorf("expected developer_instructions= in argv:\n%s", argv)
	}
	if !strings.Contains(argv, "Docker container") {
		t.Errorf("expected 'Docker container' in developer_instructions:\n%s", argv)
	}
	if !strings.Contains(argv, "Bind mounts") {
		t.Errorf("expected 'Bind mounts' in developer_instructions:\n%s", argv)
	}
}

// TestCodex_DeveloperInstructions_AppendPrompt verifies that
// [llm].append_system_prompt content appears in developer_instructions.
func TestCodex_DeveloperInstructions_AppendPrompt(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
append_system_prompt = "Custom instructions from TOML"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "Custom instructions from TOML") {
		t.Errorf("expected append_system_prompt content in developer_instructions:\n%s", argv)
	}
}

// TestCodex_DeveloperInstructions_NoAppendPrompt verifies that container
// context is injected even when no TOML prompt is configured.
func TestCodex_DeveloperInstructions_NoAppendPrompt(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "developer_instructions=") {
		t.Errorf("expected developer_instructions even without TOML prompt:\n%s", argv)
	}
	if !strings.Contains(argv, "Docker container") {
		t.Errorf("expected container context even without TOML prompt:\n%s", argv)
	}
}

// TestCodex_DeveloperInstructions_EscapesSpecialChars verifies that quotes
// and backslashes in the append prompt are escaped for the TOML CLI value.
func TestCodex_DeveloperInstructions_EscapesSpecialChars(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
append_system_prompt = 'say "hello" and use C:\path'
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, `\"hello\"`) {
		t.Errorf("expected escaped quotes in developer_instructions:\n%s", argv)
	}
	if !strings.Contains(argv, `C:\\path`) {
		t.Errorf("expected escaped backslash in developer_instructions:\n%s", argv)
	}
}

// TestCodex_SystemPromptWarning verifies that when [llm].system_prompt is
// configured, cell codex emits a warning that it cannot replace the built-in
// prompt.
func TestCodex_SystemPromptWarning(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
[llm]
system_prompt = "Replace me"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "codex", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex --dry-run failed: %v\noutput: %s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "system_prompt is set but Codex has no way to replace") {
		t.Errorf("expected base-prompt warning in output:\n%s", output)
	}
}
