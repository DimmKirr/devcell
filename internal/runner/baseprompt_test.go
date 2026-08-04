package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

// After CELL-408 the two layers resolve from different sources:
//   - system_prompt      -> base, REPLACES Claude Code's built-in prompt
//   - append_system_prompt -> overlay, layers on top
//
// The overlay must therefore ignore system_prompt entirely.
func TestResolveAppendPrompt_PrecedenceAcrossTiers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flagFile := write("flag.md", "from-append-flag-file")
	envFile := write("env.md", "from-append-env-file")
	tomlFile := write("toml.md", "from-append-toml-file")

	full := ResolveOpts{
		AppendFlagFile:   flagFile,
		AppendFlagInline: "from-append-flag-inline",
		AppendEnvFile:    envFile,
		AppendEnvInline:  "from-append-env-inline",
		CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{
			AppendSystemPromptFile: tomlFile,
			AppendSystemPrompt:     "from-append-toml-inline",
		}},
	}

	tests := []struct {
		name string
		mut  func(*ResolveOpts)
		want string
	}{
		{"flag file wins", func(o *ResolveOpts) { o.AppendFlagInline = "" }, "from-append-flag-file"},
		{"flag inline next", func(o *ResolveOpts) { o.AppendFlagFile = "" }, "from-append-flag-inline"},
		{"env file next", func(o *ResolveOpts) {
			o.AppendFlagFile, o.AppendFlagInline, o.AppendEnvInline = "", "", ""
		}, "from-append-env-file"},
		{"env inline next", func(o *ResolveOpts) {
			o.AppendFlagFile, o.AppendFlagInline, o.AppendEnvFile = "", "", ""
		}, "from-append-env-inline"},
		{"toml file next", func(o *ResolveOpts) {
			o.AppendFlagFile, o.AppendFlagInline, o.AppendEnvFile, o.AppendEnvInline = "", "", "", ""
			o.CellCfg.LLM.AppendSystemPrompt = ""
		}, "from-append-toml-file"},
		{"toml inline last", func(o *ResolveOpts) {
			o.AppendFlagFile, o.AppendFlagInline, o.AppendEnvFile, o.AppendEnvInline = "", "", "", ""
			o.CellCfg.LLM.AppendSystemPromptFile = ""
		}, "from-append-toml-inline"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := full
			tc.mut(&opts)
			got, err := ResolveAppendPrompt(opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.TrimSpace(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// system_prompt must never leak into the overlay — it is the base now.
func TestResolveAppendPrompt_IgnoresBaseSources(t *testing.T) {
	got, err := ResolveAppendPrompt(ResolveOpts{
		FlagInline: "this is the BASE",
		CellCfg:    cfg.CellConfig{LLM: cfg.LLMSection{SystemPrompt: "also base"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("overlay resolved from base sources: %q", got)
	}
}

func TestResolveAppendPrompt_AmbiguousWithinTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ResolveOpts
	}{
		{"flags", ResolveOpts{AppendFlagInline: "a", AppendFlagFile: "/x.md"}},
		{"env", ResolveOpts{AppendEnvInline: "a", AppendEnvFile: "/x.md"}},
		{"toml", ResolveOpts{CellCfg: cfg.CellConfig{LLM: cfg.LLMSection{
			AppendSystemPrompt: "a", AppendSystemPromptFile: "/x.md",
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveAppendPrompt(tc.opts); err == nil {
				t.Error("expected mutually-exclusive error")
			}
		})
	}
}

// The base file is only written when a base is actually configured —
// otherwise the stock prompt must stay in effect.
func TestWriteBasePrompt_EmptyWhenUnconfigured(t *testing.T) {
	c := promptFileConfig(t, "main")

	path, err := WriteBasePrompt(c, ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("expected no base path when unconfigured, got %q", path)
	}
	if _, statErr := os.Stat(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "system-prompt.md")); statErr == nil {
		t.Error("base prompt file must not be written when unconfigured")
	}
}

func TestWriteBasePrompt_WritesVerbatimWithoutContainerContext(t *testing.T) {
	c := promptFileConfig(t, "main")

	path, err := WriteBasePrompt(c, ResolveOpts{FlagInline: "you are a release bot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/devcell-85/.devcell/prompts/main/system-prompt.md" {
		t.Errorf("base container path = %q", path)
	}

	body, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "system-prompt.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if got != "you are a release bot" {
		t.Errorf("base file = %q, want the resolved prompt verbatim", got)
	}
	// Container context belongs on the overlay: it is regenerated per run and
	// must not be something a user's base prompt can displace.
	if strings.Contains(got, "Docker container") {
		t.Error("container context leaked into the base prompt")
	}
}

// The overlay must carry container context plus append sources only.
func TestWriteOverlayPrompt_UsesAppendSourcesNotBase(t *testing.T) {
	c := promptFileConfig(t, "main")

	if _, err := WriteOverlayPrompt(c, cfg.CellConfig{}, ResolveOpts{
		FlagInline:       "BASE-TEXT",
		AppendEnvInline:  "OVERLAY-TEXT",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(c.BaseDir, ".devcell", "prompts", "main", "additional-systemprompt.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "Docker container") {
		t.Error("overlay missing container context")
	}
	if !strings.Contains(got, "OVERLAY-TEXT") {
		t.Error("overlay missing append-sourced text")
	}
	if strings.Contains(got, "BASE-TEXT") {
		t.Error("base prompt leaked into the overlay")
	}
}
