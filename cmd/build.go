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
	buildCmd.Flags().Bool("no-cache", false, "disable Docker layer cache (full rebuild)")
}

func runBuild(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	noCache, _ := cmd.Flags().GetBool("no-cache")

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

	if err := buildImageWithSpinner(c.ConfigDir, noCache, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}
