package main

import "github.com/spf13/cobra"

var claudeCmd = &cobra.Command{
	Use:                "claude [args...]",
	Short:              "Run Claude Code in a devcell container",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("claude", []string{"--dangerously-skip-permissions"}, args)
	},
}
