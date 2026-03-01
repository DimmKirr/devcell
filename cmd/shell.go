package main

import "github.com/spf13/cobra"

var shellCmd = &cobra.Command{
	Use:   "shell [-- command [args...]]",
	Short: "Open an interactive shell in a devcell container",
	Long: `Opens an interactive bash shell inside a devcell container.

The current working directory is mounted as /workspace. Optionally pass a
command after -- to run it non-interactively instead of starting a shell.

Examples:

    cell shell
    cell shell -- ls /workspace`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Find the -- separator. Everything after it is the command to run
		// in the container; everything before it may be devcell flags.
		for i, a := range args {
			if a == "--" {
				rest := args[i+1:]
				cellFlags := args[:i]
				if len(rest) > 0 {
					return runAgent(rest[0], nil, append(cellFlags, rest[1:]...))
				}
				return runAgent("bash", nil, cellFlags)
			}
		}
		return runAgent("bash", nil, args)
	},
}
