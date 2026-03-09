package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
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
use_ollama = true in the [claude] section of devcell.toml.

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
// When --ollama flag or [claude] use_ollama=true is set, it injects
// env vars that redirect Claude Code's API calls to a local ollama instance.
func claudeEnv() map[string]string {
	dbg := scanFlag("--debug")
	useOllama := scanFlag("--ollama")

	if !useOllama {
		c, err := config.LoadFromOS()
		if err == nil {
			cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
			useOllama = cellCfg.Claude.UseOllama
		}
	}

	if !useOllama {
		return nil
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " claude: ollama mode enabled, redirecting API to host ollama\n")
	}
	return map[string]string{
		"ANTHROPIC_BASE_URL":                      "http://host.docker.internal:11434",
		"ANTHROPIC_AUTH_TOKEN":                     "ollama",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
}
