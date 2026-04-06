package main

import (
	"fmt"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build (or rebuild) the local devcell image",
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().Bool("update", false, "update nix flake inputs and rebuild without cache")
	buildCmd.Flags().Bool("no-generate", false, "skip regenerating build context (flake.nix, Dockerfile, etc.)")
}

func runBuild(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	update, _ := cmd.Flags().GetBool("update")
	noGenerate, _ := cmd.Flags().GetBool("no-generate")

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := config.EnsureBuildDir(c.BuildDir); err != nil {
		return fmt.Errorf("ensure build dir: %w", err)
	}

	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	ux.Debugf("BuildDir: %s", c.BuildDir)
	if cellCfg.Cell.NixhomePath != "" {
		ux.Debugf("NixhomePath: %s (from config/env)", cellCfg.Cell.NixhomePath)
	}

	// Sync local nixhome into build context when nixhome path is set.
	if nixhomePath := cellCfg.Cell.NixhomePath; nixhomePath != "" {
		ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
		if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}

	if !noGenerate {
		// Regenerate all build artifacts from merged config (flake.nix,
		// Dockerfile, package.json, pyproject.toml) so that stack/modules
		// changes in devcell.toml take effect without re-running cell init.
		if err := scaffold.RegenerateBuildContext(c.BuildDir, cellCfg); err != nil {
			return fmt.Errorf("regenerate build context: %w", err)
		}
	}

	if update {
		if err := updateFlakeLockWithSpinner(c.BuildDir, false, "Updating nix flake inputs"); err != nil {
			return err
		}
	}

	if err := buildImageWithSpinner(c.BuildDir, update, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}
