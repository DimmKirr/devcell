package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:   "codex [args...]",
	Short: "Run Codex in a devcell container",
	Long: `Starts an OpenAI Codex session inside an isolated devcell container.

The current working directory is mounted as /workspace. All additional
args are forwarded to the codex binary unchanged.

Examples:

    cell codex
    cell codex --model o4-mini`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("codex",
			[]string{"--dangerously-bypass-approvals-and-sandbox", "--oss", "-p", "lms"},
			args, nil)
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
