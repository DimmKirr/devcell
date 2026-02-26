package main

import "github.com/spf13/cobra"

var shellCmd = &cobra.Command{
	Use:                "shell [-- command [args...]]",
	Short:              "Open an interactive shell in a devcell container",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("bash", nil, args)
	},
}
