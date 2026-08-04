package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

func sampleConfig() config.Config {
	return config.Config{
		AppName:  "devcell-85",
		BaseDir:  "/Users/dmitry/dev/dimmkirr/devcell",
		HostUser: "dmitry",
		HostHome: "/Users/dmitry",
	}
}

func TestContainerContext_DescribesMountsAndConstraints(t *testing.T) {
	cellCfg := cfg.CellConfig{
		Volumes: []cfg.VolumeMount{
			{Mount: "~/work/secrets:/run/secrets:ro"},
		},
	}

	ctx := ContainerContext(sampleConfig(), cellCfg)

	checks := map[string]string{
		"container identity":  "Docker container",
		"project alias":       "/devcell-85",
		"host base dir":       "/Users/dmitry/dev/dimmkirr/devcell",
		"same filesystem":     "same filesystem",
		"persistent home":     "/home/dmitry",
		"skills mount":        ".claude/skills",
		"user volume":         "/run/secrets",
		"user volume ro":      "read-only",
		"host mapping prefix": "host: /Users/dmitry/dev/dimmkirr/devcell",
		"nix constraint":      "/opt/devcell",
	}

	for name, want := range checks {
		if !strings.Contains(ctx, want) {
			t.Errorf("%s: container context missing %q\n\nFull:\n%s", name, want, ctx)
		}
	}
}

func TestContainerContext_NoProjectContextWrapper(t *testing.T) {
	// `Project context:` was the legacy wrapper that glued user prompt
	// content into the container preamble. ContainerContext now contains
	// only container facts — the wrapper is gone (resolver outputs the
	// user prompt as a separate concatenated layer).
	cellCfg := cfg.CellConfig{
		LLM: cfg.LLMSection{SystemPrompt: "use postgres 16"},
	}
	ctx := ContainerContext(sampleConfig(), cellCfg)
	if strings.Contains(ctx, "Project context:") {
		t.Errorf("ContainerContext leaked the legacy `Project context:` wrapper:\n%s", ctx)
	}
	if strings.Contains(ctx, "use postgres 16") {
		t.Errorf("ContainerContext leaked user prompt content:\n%s", ctx)
	}
}

func TestResolveSystemPrompt_PrecedenceAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	flagFile := filepath.Join(dir, "flag.md")
	envFile := filepath.Join(dir, "env.md")
	tomlFile := filepath.Join(dir, "toml.md")
	if err := os.WriteFile(flagFile, []byte("from-flag-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("from-env-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlFile, []byte("from-toml-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		opts    ResolveOpts
		want    string
		wantErr string
	}{
		{name: "all empty", want: ""},

		{
			name: "tier 1: flag file wins over everything",
			opts: ResolveOpts{
				FlagFile:   flagFile,
				FlagInline: "",
				EnvFile:    envFile,
				EnvInline:  "from-env-inline",
				CellCfg:    cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "from-toml-inline"}},
			},
			want: "from-flag-file\n",
		},
		{
			name: "tier 2: flag inline wins when no flag file",
			opts: ResolveOpts{
				FlagInline: "from-flag-inline",
				EnvFile:    envFile,
				EnvInline:  "from-env-inline",
				CellCfg:    cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "from-toml-inline"}},
			},
			want: "from-flag-inline",
		},
		{
			name: "tier 3: env file when no flag (within tier inline must be empty)",
			opts: ResolveOpts{
				EnvFile: envFile,
				CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "from-toml-inline"}},
			},
			want: "from-env-file\n",
		},
		{
			name: "tier 4: env inline wins when no env file",
			opts: ResolveOpts{
				EnvInline: "from-env-inline",
				CellCfg:   cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "from-toml-inline"}},
			},
			want: "from-env-inline",
		},
		{
			name: "tier 5: toml file when no env",
			opts: ResolveOpts{
				CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{SystemPromptFile: tomlFile}},
			},
			want: "from-toml-file\n",
		},
		{
			name: "tier 6: toml inline when no toml file",
			opts: ResolveOpts{
				CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "from-toml-inline"}},
			},
			want: "from-toml-inline",
		},

		{
			name:    "ambiguity: flag file + flag inline",
			opts:    ResolveOpts{FlagFile: flagFile, FlagInline: "x"},
			wantErr: "--system-prompt and --system-prompt-file are mutually exclusive",
		},
		{
			name:    "ambiguity: env file + env inline",
			opts:    ResolveOpts{EnvFile: envFile, EnvInline: "x"},
			wantErr: "DEVCELL_SYSTEM_PROMPT and DEVCELL_SYSTEM_PROMPT_FILE",
		},
		{
			name: "ambiguity: TOML file + TOML inline",
			opts: ResolveOpts{
				CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{
					SystemPrompt:     "x",
					SystemPromptFile: tomlFile,
				}},
			},
			wantErr: "[llm].system_prompt and [llm].system_prompt_file",
		},

		{
			name: "higher tier silences lower-tier ambiguity",
			opts: ResolveOpts{
				FlagInline: "winning",
				EnvFile:    envFile,
				EnvInline:  "ambiguous",
			},
			want: "winning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSystemPrompt(tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (value=%q)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveSystemPrompt_TomlFileRelativePath(t *testing.T) {
	// `[llm].system_prompt_file = "./SYSTEM.md"` should resolve relative
	// to the project's base dir (where devcell.toml lives).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SYSTEM.md"), []byte("from-relative\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSystemPrompt(ResolveOpts{
		CellCfg:    cfg.CellConfig{LLM: cfg.LLMSection{SystemPromptFile: "SYSTEM.md"}},
		CfgBaseDir: dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-relative\n" {
		t.Fatalf("got %q, want %q", got, "from-relative\n")
	}
}

func TestResolveSystemPrompt_FileNotFound(t *testing.T) {
	_, err := ResolveSystemPrompt(ResolveOpts{FlagFile: "/no/such/file"})
	if err == nil {
		t.Fatal("expected error for missing flag file, got nil")
	}
	if !strings.Contains(err.Error(), "--system-prompt-file") {
		t.Fatalf("error lost source context: %v", err)
	}
}

func TestAssembleOverlayPrompt_PrependsContainerContext(t *testing.T) {
	out, err := AssembleOverlayPrompt(sampleConfig(), cfg.CellConfig{}, ResolveOpts{
		AppendFlagInline: "be terse",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Docker container") {
		t.Error("assembled prompt missing container context")
	}
	if !strings.Contains(out, "be terse") {
		t.Error("assembled prompt missing resolved user prompt")
	}
	envIdx := strings.Index(out, "Docker container")
	userIdx := strings.Index(out, "be terse")
	if userIdx <= envIdx {
		t.Error("user prompt should appear after container context")
	}
}

func TestAssembleOverlayPrompt_EmptyResolverReturnsContextOnly(t *testing.T) {
	out, err := AssembleOverlayPrompt(sampleConfig(), cfg.CellConfig{}, ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Docker container") {
		t.Error("assembled prompt missing container context")
	}
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("expected no trailing blank when resolver empty, got %q", out)
	}
}
