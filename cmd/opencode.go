package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/spf13/cobra"
)

const opencodeJSONStub = `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {}
}
`

var opencodeCmd = &cobra.Command{
	Use:   "opencode [args...]",
	Short: "Run OpenCode in a devcell container",
	Long: `Starts an OpenCode session inside an isolated devcell container.

The current working directory is mounted as /workspace. A minimal
opencode.json is scaffolded automatically if one does not already exist.
All additional args are forwarded to the opencode binary unchanged.

Examples:

    cell opencode
    cell opencode --model anthropic/claude-sonnet-4-5`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		scaffoldOpencodeJSON()
		return runAgent("opencode",
			[]string{"--dangerously-bypass-approvals-and-sandbox"},
			args)
	},
}

var opencodeResumeCmd = &cobra.Command{
	Use:   "resume [args...]",
	Short: "Resume an OpenCode session",
	Long: `Resumes a previous OpenCode session inside a devcell container.

All additional args are forwarded to 'opencode resume' unchanged.

Examples:

    cell opencode resume`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		scaffoldOpencodeJSON()
		return runAgent("opencode", nil, append([]string{"resume"}, args...))
	},
}

func init() {
	opencodeCmd.AddCommand(opencodeResumeCmd)
}

// scaffoldOpencodeJSON writes a minimal opencode.json to CellHome if absent.
// Non-fatal — logs a warning on failure.
func scaffoldOpencodeJSON() {
	c, err := config.LoadFromOS()
	if err != nil {
		return
	}
	target := c.CellHome + "/opencode.json"
	if _, err := os.Stat(target); err == nil {
		return // already exists
	}
	if err := os.MkdirAll(c.CellHome, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: opencode scaffold: mkdir %s: %v\n", c.CellHome, err)
		return
	}
	if err := os.WriteFile(target, []byte(opencodeJSONStub), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: opencode scaffold: write opencode.json: %v\n", err)
	}
}
