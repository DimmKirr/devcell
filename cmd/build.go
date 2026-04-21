package main

import (
	"fmt"
	"os"

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

	// ── Vagrant engine ────────────────────────────────────────────────────────
	// cell build --engine=vagrant   → vagrant provision (re-applies nixhome flake)
	// cell build --update --engine=vagrant → nix flake update inside VM, then provision
	engine := scanStringFlag("--engine")
	if scanFlag("--macos") {
		engine = "vagrant"
	}
	if engine == "vagrant" {
		cellCfgVagrant := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		vagrantBox := scanStringFlag("--vagrant-box")
		if vagrantBox == "" {
			vagrantBox = "utm/bookworm"
		}
		vagrantProvider := scanStringFlag("--vagrant-provider")
		if vagrantProvider == "" {
			vagrantProvider = "utm"
		}
		// Scaffold Vagrantfile idempotently (same as runVagrantAgent step 1).
		nixhomeDir := resolveVagrantNixhome(c.BaseDir)
		if nixhomeDir == "" {
			nixhomeDir = c.BaseDir + "/nixhome"
		}
		vmConfigDir := os.Getenv("DEVCELL_CONFIG_DIR")
		if vmConfigDir == "" {
			vmConfigDir = c.HostHome + "/.config/devcell"
		}
		// Always regenerate Vagrantfile on build (ports, stack may have changed).
		os.Remove(c.BuildDir + "/Vagrantfile")
		if err := scaffold.ScaffoldLinuxVagrantfile(
			c.BuildDir, vagrantBox, vagrantProvider,
			cellCfgVagrant.Cell.ResolvedStack(),
			c.BaseDir, nixhomeDir,
			c.VNCPort, c.RDPPort,
			c.HostHome, vmConfigDir,
		); err != nil {
			fmt.Fprintf(os.Stderr, "warning: vagrantfile scaffold failed: %v\n", err)
		}
		return runVagrantBuild(c.BuildDir, c.BaseDir, cellCfgVagrant, update, scanFlag("--dry-run"))
	}

	// ── Docker engine (default) ───────────────────────────────────────────────
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
