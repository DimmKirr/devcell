package main

import "github.com/spf13/cobra"

var claudeCmd = &cobra.Command{
	Use:   "claude [args...]",
	Short: "Run Claude Code in a devcell container",
	Long: `Starts a Claude Code session inside an isolated devcell container.

The current working directory is mounted as /workspace. All additional
args are forwarded to the claude binary unchanged.

Examples:

    cell claude
    cell claude --resume`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("claude", []string{"--dangerously-skip-permissions"}, args, nil)
	},
}
