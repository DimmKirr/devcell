package runner

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

func TestBuildSystemPrompt(t *testing.T) {
	c := config.Config{
		AppName:  "devcell-85",
		BaseDir:  "/Users/dmitry/dev/dimmkirr/devcell",
		HostUser: "dmitry",
		HostHome: "/Users/dmitry",
	}
	cellCfg := cfg.CellConfig{
		Volumes: []cfg.VolumeMount{
			{Mount: "~/work/secrets:/run/secrets:ro"},
		},
	}

	prompt := BuildSystemPrompt(c, cellCfg)

	checks := []struct {
		name string
		want string
	}{
		{"container identity", "Docker container"},
		{"project alias", "/devcell-85"},
		{"host base dir", "/Users/dmitry/dev/dimmkirr/devcell"},
		{"same filesystem", "same filesystem"},
		{"host path mapping", "host paths"},
		{"persistent home", "/home/dmitry"},
		{"skills mount", ".claude/skills"},
		{"user volume", "/run/secrets"},
		{"user volume ro", "read-only"},
		{"host mapping", "host: /Users/dmitry/dev/dimmkirr/devcell"},
		{"nix constraint", "/opt/devcell"},
	}

	for _, tc := range checks {
		if !strings.Contains(prompt, tc.want) {
			t.Errorf("%s: prompt missing %q\n\nFull prompt:\n%s", tc.name, tc.want, prompt)
		}
	}
}

func TestBuildSystemPrompt_WithCustomPrompt(t *testing.T) {
	c := config.Config{
		AppName:  "myproject-1",
		BaseDir:  "/Users/dev/myproject",
		HostUser: "dev",
		HostHome: "/Users/dev",
	}
	cellCfg := cfg.CellConfig{
		LLM: cfg.LLMSection{
			SystemPrompt: "This project uses PostgreSQL 16 with pgx/v5.\nAPI at /api/v2/.",
		},
	}

	prompt := BuildSystemPrompt(c, cellCfg)

	if !strings.Contains(prompt, "Project context:") {
		t.Error("prompt should contain 'Project context:' header")
	}
	if !strings.Contains(prompt, "PostgreSQL 16") {
		t.Error("prompt should contain custom system prompt content")
	}
	if !strings.Contains(prompt, "/api/v2/") {
		t.Error("prompt should contain custom system prompt content")
	}
	// Custom prompt should come after container environment
	envIdx := strings.Index(prompt, "Docker container")
	customIdx := strings.Index(prompt, "Project context:")
	if customIdx <= envIdx {
		t.Error("custom prompt should appear after container environment section")
	}
}

func TestBuildSystemPrompt_EmptyCustomPrompt(t *testing.T) {
	c := config.Config{
		AppName:  "myproject-1",
		BaseDir:  "/Users/dev/myproject",
		HostUser: "dev",
		HostHome: "/Users/dev",
	}
	cellCfg := cfg.CellConfig{}

	prompt := BuildSystemPrompt(c, cellCfg)

	if strings.Contains(prompt, "Project context:") {
		t.Error("prompt should NOT contain 'Project context:' when system_prompt is empty")
	}
}
