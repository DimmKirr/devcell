package main

import (
	"context"
	"fmt"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/spf13/cobra"
)

var startDetach bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a devcell container in the background",
	Long: `Starts a devcell container running in the background.
Use 'cell shell' to attach, 'cell stop' to shut it down.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyOutputFlags()
		c, err := config.LoadFromOS()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if runner.ContainerRunning(context.Background(), c.ContainerName) {
			fmt.Printf("Container %s is already running\n", c.ContainerName)
			return nil
		}
		startDetach = true
		defer func() { startDetach = false }()
		return runAgent("sleep", []string{"infinity"}, nil, nil)
	},
}
