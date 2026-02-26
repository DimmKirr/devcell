package main

import (
	"fmt"

	"github.com/DimmKirr/devcell/internal/config"
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

	if err := buildImageWithSpinner(c.ConfigDir, noCache, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}
