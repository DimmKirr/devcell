package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:                "codex [args...]",
	Short:              "Run Codex in a devcell container",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("codex",
			[]string{"--dangerously-bypass-approvals-and-sandbox", "--oss", "-p", "lms"},
			args)
	},
}

var codexResumeCmd = &cobra.Command{
	Use:                "resume [args...]",
	Short:              "Resume a Codex session",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("codex", nil, append([]string{"resume"}, args...))
	},
}

func init() {
	codexCmd.AddCommand(codexResumeCmd)
}
