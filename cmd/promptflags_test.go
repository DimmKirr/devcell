package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

func promptFlagsConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppName:  "devcell-85",
		BaseDir:  t.TempDir(),
		CellName: "main",
		HostUser: "dmitry",
		HostHome: "/Users/dmitry",
	}
}

// The prompt reaches claude as a file path, never as an inline argv element:
// inline capped it at MAX_ARG_STRLEN and published it to `ps aux`.
func TestClaudePromptFlags_EmitsAppendSystemPromptFile(t *testing.T) {
	c := promptFlagsConfig(t)

	flags, err := claudePromptFlags(c, cfg.CellConfig{}, runner.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"--append-system-prompt-file", "/devcell-85/.devcell/prompts/main/additional-systemprompt.md"}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v, want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q, want %q", i, flags[i], want[i])
		}
	}
}

func TestClaudePromptFlags_NeverEmitsInlineForm(t *testing.T) {
	c := promptFlagsConfig(t)

	flags, err := claudePromptFlags(c, cfg.CellConfig{}, runner.ResolveOpts{AppendEnvInline: "be terse"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// claude rejects the inline and file forms together, so only the file
	// form may ever be emitted.
	for _, f := range flags {
		if f == "--append-system-prompt" {
			t.Fatalf("inline --append-system-prompt must not be emitted, got %v", flags)
		}
	}
	// The prompt text itself must not appear in argv.
	for _, f := range flags {
		if strings.Contains(f, "be terse") {
			t.Errorf("prompt text leaked into argv: %v", flags)
		}
	}
}

func TestClaudePromptFlags_WritesResolvedPromptToFile(t *testing.T) {
	c := promptFlagsConfig(t)

	if _, err := claudePromptFlags(c, cfg.CellConfig{}, runner.ResolveOpts{AppendEnvInline: "be terse"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "Docker container") {
		t.Error("generated overlay missing container context")
	}
	if !strings.Contains(got, "be terse") {
		t.Error("generated overlay missing resolved prompt")
	}
}

func TestClaudePromptFlags_PropagatesResolverError(t *testing.T) {
	c := promptFlagsConfig(t)

	_, err := claudePromptFlags(c, cfg.CellConfig{}, runner.ResolveOpts{
		EnvInline: "a",
		EnvFile:   "/nonexistent/b.md",
	})
	if err == nil {
		t.Fatal("expected ambiguous-source error to propagate, got nil")
	}
}

// A configured base replaces Claude Code's built-in prompt, so it travels on
// --system-prompt-file alongside the overlay.
func TestClaudePromptFlags_EmitsBaseFlagWhenConfigured(t *testing.T) {
	c := promptFlagsConfig(t)

	flags, err := claudePromptFlags(c, cfg.CellConfig{
		LLM: cfg.LLMSection{SystemPrompt: "you are a release bot"},
	}, runner.ResolveOpts{
		CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "you are a release bot"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(flags, " ")
	if !strings.Contains(joined, "--system-prompt-file /devcell-85/.devcell/prompts/main/system-prompt.md") {
		t.Errorf("expected base flag, got %v", flags)
	}
	if !strings.Contains(joined, "--append-system-prompt-file /devcell-85/.devcell/prompts/main/additional-systemprompt.md") {
		t.Errorf("expected overlay flag, got %v", flags)
	}
}

// Unconfigured base must leave the stock prompt in effect — no flag at all.
func TestClaudePromptFlags_NoBaseFlagWhenUnconfigured(t *testing.T) {
	c := promptFlagsConfig(t)

	flags, err := claudePromptFlags(c, cfg.CellConfig{}, runner.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range flags {
		if f == "--system-prompt-file" {
			t.Fatalf("base flag must not be emitted when unconfigured, got %v", flags)
		}
	}
}

// Base and overlay land in separate files: container context belongs only to
// the overlay, and the base must not be polluted by it.
func TestClaudePromptFlags_BaseAndOverlayAreSeparateFiles(t *testing.T) {
	c := promptFlagsConfig(t)
	llm := cfg.LLMSection{SystemPrompt: "BASE-ONLY", AppendSystemPrompt: "OVERLAY-ONLY"}

	if _, err := claudePromptFlags(c, cfg.CellConfig{LLM: llm}, runner.ResolveOpts{
		CellCfg: cfg.CellConfig{LLM: llm},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	base := read("system-prompt.md")
	overlay := read("additional-systemprompt.md")

	if base != "BASE-ONLY" {
		t.Errorf("base file = %q, want verbatim base prompt", base)
	}
	if strings.Contains(base, "Docker container") {
		t.Error("container context leaked into the base file")
	}
	if !strings.Contains(overlay, "Docker container") || !strings.Contains(overlay, "OVERLAY-ONLY") {
		t.Errorf("overlay file = %q, want container context + append text", overlay)
	}
	if strings.Contains(overlay, "BASE-ONLY") {
		t.Error("base prompt leaked into the overlay file")
	}
}
