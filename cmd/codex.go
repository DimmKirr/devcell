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
		extraFlags, extraEnv := codexOllamaConfig()
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

// codexOllamaConfig returns extra CLI flags and env vars when ollama mode is
// active (use_ollama=true in devcell.toml, or --ollama flag).
// Returns nil, nil when ollama is not configured — Codex runs normally.
func codexOllamaConfig() (flags []string, env map[string]string) {
	dbg := scanFlag("--debug")
	useOllama := scanFlag("--ollama")

	var model string
	if !useOllama {
		c, err := config.LoadFromOS()
		if err == nil {
			cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
			useOllama = cellCfg.LLM.UseOllama
			model = cellCfg.LLM.Models.Default
		}
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
