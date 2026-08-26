package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude [args...]",
	Short: "Run Claude Code in a devcell container",
	Long: `Starts a Claude Code session inside an isolated devcell container.

The current working directory is mounted as /workspace. All additional
args are forwarded to the claude binary unchanged.

Use --ollama to route Claude Code through a local ollama instance
(Anthropic Messages API compatibility). This sets ANTHROPIC_BASE_URL
to point at ollama on the host. Can also be enabled permanently via
use_ollama = true in the [llm] section of devcell.toml.

Use --openrouter to route Claude Code through OpenRouter. Requires
OPENROUTER_API_KEY env var. Can also be enabled permanently via
use_openrouter = true in the [llm] section of devcell.toml.

The model is resolved in order:
  1. [llm.models] default in devcell.toml (e.g. "ollama/qwen3:30b")
  2. Best-ranked model from the running ollama instance (auto-detect)

Examples:

    cell claude
    cell claude --resume
    cell claude --ollama
    cell claude --openrouter`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("claude", []string{"--dangerously-skip-permissions"}, args, claudeEnv())
	},
}

// claudeEnv returns extra env vars for the claude container.
// When --ollama flag or [llm] use_ollama=true is set, it injects env vars
// that redirect Claude Code's API calls to a local ollama instance and
// sets ANTHROPIC_MODEL to the configured or best-available model.
// When --openrouter flag or [llm] use_openrouter=true is set, it injects
// env vars that redirect Claude Code's API calls through OpenRouter.
func claudeEnv() map[string]string {
	dbg := scanFlag("--debug")
	useOllama := scanFlag("--ollama")
	useOpenRouter := scanFlag("--openrouter")

	// Always load config — needed for use_ollama, use_openrouter, and model selection.
	var configModel string
	var models cfg.LLMModelsSection
	c, err := config.LoadFromOS()
	if err == nil {
		cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		if !useOllama {
			useOllama = cellCfg.LLM.UseOllama
		}
		if !useOpenRouter {
			useOpenRouter = cellCfg.LLM.UseOpenRouter
		}
		configModel = cellCfg.LLM.Models.Default
		models = cellCfg.LLM.Models
	}

	// Base env vars for all claude sessions.
	env := map[string]string{}

	if useOpenRouter {
		for k, v := range openrouterEnv(configModel, models, dbg) {
			env[k] = v
		}
		return env
	}

	if !useOllama {
		return env
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " claude: ollama mode enabled, redirecting API to host ollama\n")
	}

	env["ANTHROPIC_BASE_URL"] = "http://host.docker.internal:11434"
	env["ANTHROPIC_AUTH_TOKEN"] = "ollama"
	env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"

	if model := resolveOllamaModel(configModel, dbg); model != "" {
		env["ANTHROPIC_MODEL"] = model
	}

	return env
}

// openrouterEnv returns env vars that redirect Claude Code through OpenRouter.
// The API key is resolved lazily (after 1Password) via ResolveOpenRouterKey.
//
// Model resolution order:
//  1. [llm.models] default with "openrouter/" prefix (explicit openrouter default)
//  2. [llm.models] default without provider prefix (provider-neutral default)
//  3. First model in [llm.models.providers.openrouter] models list
//  4. No model override (Claude Code uses its own default)
func openrouterEnv(configModel string, models cfg.LLMModelsSection, dbg bool) map[string]string {
	if dbg {
		fmt.Fprintf(os.Stderr, " claude: openrouter mode enabled, redirecting API to openrouter.ai\n")
	}

	env := map[string]string{
		"ANTHROPIC_BASE_URL":                         openRouterAnthropicBaseURL,
		"ANTHROPIC_API_KEY":                          "",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
		"CLAUDE_CODE_SKIP_FAST_MODE_ORG_CHECK":       "1",
	}

	model := resolveOpenRouterModel(configModel, models, dbg)
	if model != "" {
		env["ANTHROPIC_MODEL"] = model
	}

	return env
}

// resolveOpenRouterModel picks the model for OpenRouter mode.
func resolveOpenRouterModel(configModel string, models cfg.LLMModelsSection, dbg bool) string {
	// Priority 1: global default with openrouter/ prefix.
	if strings.HasPrefix(configModel, "openrouter/") {
		model := strings.TrimPrefix(configModel, "openrouter/")
		if dbg {
			fmt.Fprintf(os.Stderr, " claude: openrouter model from config default: %s\n", model)
		}
		return model
	}

	// Priority 2: global default without any provider prefix (e.g. "google/gemini-2.5-pro").
	if configModel != "" && !strings.HasPrefix(configModel, "ollama/") {
		if dbg {
			fmt.Fprintf(os.Stderr, " claude: openrouter model from config default: %s\n", configModel)
		}
		return configModel
	}

	// Priority 3: first model in [llm.models.providers.openrouter].
	if p, ok := models.Providers["openrouter"]; ok && len(p.Models) > 0 {
		model := p.Models[0]
		if dbg {
			fmt.Fprintf(os.Stderr, " claude: openrouter model from providers list: %s\n", model)
		}
		return model
	}

	// No model override: skip ollama model, let Claude Code use its default.
	if configModel != "" && dbg {
		fmt.Fprintf(os.Stderr, " claude: ignoring ollama model %q in openrouter mode, using Claude Code default\n", configModel)
	}
	return ""
}

// ResolveOpenRouterKey fills ANTHROPIC_AUTH_TOKEN and OPENROUTER_API_KEY from
// the environment. Called after 1Password resolution so the key is available.
func ResolveOpenRouterKey(env map[string]string) error {
	if err := FillOpenRouterKey(env); err != nil {
		return err
	}
	env["ANTHROPIC_AUTH_TOKEN"] = env["OPENROUTER_API_KEY"]
	return nil
}

// resolveOllamaModel returns the bare ollama model name to use as ANTHROPIC_MODEL.
// Priority: config [llm.models] default > best-ranked model from running ollama.
// Returns "" if no model can be determined (ollama unreachable, no models).
func resolveOllamaModel(configModel string, dbg bool) string {
	if configModel != "" {
		// Strip "ollama/" prefix produced by FormatActiveTOMLSnippet.
		model := strings.TrimPrefix(configModel, "ollama/")
		if dbg {
			if model != configModel {
				fmt.Fprintf(os.Stderr, " claude: model from config: %s (stripped ollama/ prefix from %q)\n", model, configModel)
			} else {
				fmt.Fprintf(os.Stderr, " claude: model from config: %s\n", model)
			}
		}
		return model
	}

	// Auto-detect: probe local ollama and pick the best-ranked model.
	if dbg {
		fmt.Fprintf(os.Stderr, " claude: no model in config — auto-selecting from local ollama\n")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if !ollama.Detect(ctx, ollama.DefaultBaseURL) {
		if dbg {
			fmt.Fprintf(os.Stderr, " claude: ollama not reachable at %s — no model set\n", ollama.DefaultBaseURL)
		}
		return ""
	}
	if dbg {
		fmt.Fprintf(os.Stderr, " claude: ollama reachable at %s\n", ollama.DefaultBaseURL)
	}

	models, err := ollama.FetchModels(ctx, ollama.DefaultBaseURL)
	if err != nil {
		if dbg {
			fmt.Fprintf(os.Stderr, " claude: fetch models failed: %v\n", err)
		}
		return ""
	}
	if dbg {
		fmt.Fprintf(os.Stderr, " claude: %d model(s) available\n", len(models))
	}
	if len(models) == 0 {
		return ""
	}

	// Rank local models with real system RAM so the composite score
	// penalises models that won't fit (same algo as `cell models`).
	systemRAM := ollama.GetSystemRAMGB()
	if dbg {
		fmt.Fprintf(os.Stderr, " claude: system RAM %.0f GB — ranking by composite score (swe×0.6 + speed×0.25) × ram_fit\n", systemRAM)
	}

	ranked := ollama.RankModels(models, 0, nil, nil, systemRAM, "")
	if len(ranked) == 0 {
		return ""
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " claude: %d model(s) ranked (composite score = swe×0.6 + speed×0.25, ×0.1 if RAM tight):\n", len(ranked))
		for _, r := range ranked {
			_, needed := ollama.CheckHardwareSafe(r.ParameterSize, systemRAM)
			ramStr := "ok"
			if needed > 0 && systemRAM > 0 && needed > systemRAM*0.75 {
				ramStr = fmt.Sprintf("tight (%.0fGB needed, %.0fGB avail)", needed, systemRAM)
			} else if needed > 0 {
				ramStr = fmt.Sprintf("%.0fGB", needed)
			}
			fmt.Fprintf(os.Stderr, " claude:   [%d] %-35s  swe=%-5.1f  speed=%-6.0f  score=%.2f  ram=%s\n",
				r.Rank, r.Name, r.SWEScore, r.SpeedTPM, r.RecommendedScore, ramStr)
		}
		top := ranked[0]
		fmt.Fprintf(os.Stderr, " claude: picking %s — highest score (%.2f: swe=%.1f, speed=%.0fT/m)\n",
			top.Name, top.RecommendedScore, top.SWEScore, top.SpeedTPM)
	}

	model := ranked[0].Name
	fmt.Printf(" → ollama model: %s (set [llm.models] default in devcell.toml to pin)\n", model)
	return model
}
