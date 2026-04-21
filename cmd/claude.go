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

The model is resolved in order:
  1. [llm.models] default in devcell.toml (e.g. "ollama/qwen3:30b")
  2. Best-ranked model from the running ollama instance (auto-detect)

Examples:

    cell claude
    cell claude --resume
    cell claude --ollama`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("claude", []string{"--dangerously-skip-permissions"}, args, claudeEnv())
	},
}

// claudeEnv returns extra env vars for the claude container.
// When --ollama flag or [llm] use_ollama=true is set, it injects env vars
// that redirect Claude Code's API calls to a local ollama instance and
// sets ANTHROPIC_MODEL to the configured or best-available model.
func claudeEnv() map[string]string {
	dbg := scanFlag("--debug")
	useOllama := scanFlag("--ollama")

	// Always load config — needed for both use_ollama and model selection.
	var configModel string
	c, err := config.LoadFromOS()
	if err == nil {
		cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		if !useOllama {
			useOllama = cellCfg.LLM.UseOllama
		}
		configModel = cellCfg.LLM.Models.Default
	}

	if !useOllama {
		return nil
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " claude: ollama mode enabled, redirecting API to host ollama\n")
	}

	env := map[string]string{
		"ANTHROPIC_BASE_URL":                       "http://host.docker.internal:11434",
		"ANTHROPIC_AUTH_TOKEN":                     "ollama",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}

	if model := resolveOllamaModel(configModel, dbg); model != "" {
		env["ANTHROPIC_MODEL"] = model
	}

	return env
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
