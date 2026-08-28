package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running devcell container",
	Long: `Stops the devcell container started by 'cell start'.
The container is automatically removed after stopping.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.LoadFromOS()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		ctx := context.Background()
		if !runner.ContainerRunning(ctx, c.ContainerName) {
			fmt.Printf("No running container %s found\n", c.ContainerName)
			return nil
		}
		if err := exec.CommandContext(ctx, "docker", "stop", c.ContainerName).Run(); err != nil {
			return fmt.Errorf("stop container %s: %w", c.ContainerName, err)
		}
		fmt.Printf("Container %s stopped\n", c.ContainerName)
		return nil
	},
}
