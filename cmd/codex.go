package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/spf13/cobra"
)

var codexCmd = &cobra.Command{
	Use:   "codex [args...]",
	Short: "Run Codex in a devcell container",
	Long: `Starts an OpenAI Codex session inside an isolated devcell container.

The current working directory is mounted as /workspace. All additional
args are forwarded to the codex binary unchanged.

When use_ollama = true in the [llm] section of devcell.toml (or --ollama
is passed), Codex is started with --oss --local-provider ollama and
CODEX_OSS_BASE_URL pointing at the host ollama instance. The model from
llm.models.default is also passed when set.

Without ollama configured, Codex runs normally against the cloud provider
(requires OPENAI_API_KEY or equivalent).

Examples:

    cell codex
    cell codex --ollama
    cell codex --model o3`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		extraFlags, extraEnv := codexProviderConfig()
		return runAgent("codex",
			append([]string{"--dangerously-bypass-approvals-and-sandbox"}, extraFlags...),
			args, extraEnv)
	},
}

var codexResumeCmd = &cobra.Command{
	Use:   "resume [args...]",
	Short: "Resume a Codex session",
	Long: `Resumes a previous Codex session inside a devcell container.

All additional args are forwarded to 'codex resume' unchanged.

Examples:

    cell codex resume`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("codex", nil, append([]string{"resume"}, args...), nil)
	},
}

func init() {
	codexCmd.AddCommand(codexResumeCmd)
}

// codexProviderConfig returns extra CLI flags and env vars for the active
// provider mode. OpenRouter (--openrouter or use_openrouter=true) wins over
// ollama; with neither configured Codex runs normally against the cloud
// provider. Returns nil, nil in that default case.
func codexProviderConfig() (flags []string, env map[string]string) {
	dbg := scanFlag("--debug")
	useOllama := scanFlag("--ollama")
	useOpenRouter := scanFlag("--openrouter")

	var model string
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
		model = cellCfg.LLM.Models.Default
		models = cellCfg.LLM.Models
	}

	if useOpenRouter {
		return codexOpenRouterConfig(model, models, dbg)
	}

	if !useOllama {
		return nil, nil
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " codex: ollama mode enabled\n")
	}

	flags = []string{"--oss", "--local-provider", "ollama"}
	if model != "" {
		flags = append(flags, "--model", model)
	}

	return flags, map[string]string{
		"CODEX_OSS_BASE_URL": "http://host.docker.internal:11434/v1",
	}
}

// codexOpenRouterConfig returns -c config overrides that point Codex at
// OpenRouter's OpenAI-compat endpoint. Codex needs wire_api=responses —
// OpenRouter translates to Chat Completions for models that lack native
// Responses support. The API key is resolved lazily (after 1Password) via
// FillOpenRouterKey, requested by the empty OPENROUTER_API_KEY placeholder.
func codexOpenRouterConfig(configModel string, models cfg.LLMModelsSection, dbg bool) (flags []string, env map[string]string) {
	if dbg {
		fmt.Fprintf(os.Stderr, " codex: openrouter mode enabled\n")
	}

	flags = []string{
		"-c", "model_provider=openrouter",
		"-c", "model_providers.openrouter.name=OpenRouter",
		"-c", "model_providers.openrouter.base_url=" + openRouterOpenAIBaseURL,
		"-c", "model_providers.openrouter.env_key=OPENROUTER_API_KEY",
		"-c", "model_providers.openrouter.wire_api=responses",
	}
	if model := resolveOpenRouterModel(configModel, models, dbg); model != "" {
		flags = append(flags, "--model", model)
	}

	return flags, map[string]string{"OPENROUTER_API_KEY": ""}
}
