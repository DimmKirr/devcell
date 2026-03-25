package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build (or rebuild) the local devcell image",
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().Bool("update", false, "update nix flake inputs and rebuild without cache")
}

func runBuild(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	update, _ := cmd.Flags().GetBool("update")

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Sync local nixhome into build context when DEVCELL_NIXHOME_PATH is set.
	if nixhomePath := os.Getenv("DEVCELL_NIXHOME_PATH"); nixhomePath != "" {
		if err := scaffold.SyncNixhome(nixhomePath, c.ConfigDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}

	// Regenerate package.json and pyproject.toml from devcell.toml.
	if err := scaffold.RegeneratePackageFiles(c.ConfigDir); err != nil {
		return fmt.Errorf("regenerate package files: %w", err)
	}

	if update {
		if err := updateFlakeLockWithSpinner(c.ConfigDir, false, "Updating nix flake inputs"); err != nil {
			return err
		}
	}

	if err := buildImageWithSpinner(c.ConfigDir, update, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}
