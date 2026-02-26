package main

import "github.com/spf13/cobra"

var opencodeCmd = &cobra.Command{
	Use:                "opencode [args...]",
	Short:              "Run OpenCode in a devcell container",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("opencode",
			[]string{"--dangerously-bypass-approvals-and-sandbox"},
			args)
	},
}

var opencodeResumeCmd = &cobra.Command{
	Use:                "resume [args...]",
	Short:              "Resume an OpenCode session",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("opencode", nil, append([]string{"resume"}, args...))
	},
}

func init() {
	opencodeCmd.AddCommand(opencodeResumeCmd)
}
