package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize .devcell.toml and .devcell/ build context in current directory",
	RunE:  runInit,
	Args:  cobra.NoArgs,
}

func init() {
	initCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts and proceed with defaults")
	initCmd.Flags().Bool("macos", false, "Set up a macOS VM box via UTM + Vagrant")
	initCmd.Flags().Bool("force", false, "Overwrite existing files and update flake inputs (implies --update)")
	initCmd.Flags().Bool("update", false, "update nix flake inputs (pull latest) instead of just resolving")
	initCmd.Flags().String("nixhome", "", "nixhome source: local path or git URL (default: upstream repo)")
	initCmd.Flags().String("local-nixhome", "", "deprecated: use --nixhome instead")
	_ = initCmd.Flags().MarkHidden("local-nixhome")
	initCmd.Flags().String("stack", "", "stack name (base, go, node, python, fullstack, electronics, ultimate)")
	initCmd.Flags().StringSlice("modules", nil, "explicit module list (comma-separated, e.g. go,infra,electronics)")
}

func runInit(cmd *cobra.Command, _ []string) error {
	applyOutputFlags()
	macos, _ := cmd.Flags().GetBool("macos")
	if macos {
		return runInitMacOS()
	}
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	update, _ := cmd.Flags().GetBool("update")

	if bi, _ := cmd.Flags().GetString("base-image"); bi != "" {
		os.Setenv("DEVCELL_BASE_IMAGE", bi)
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	stack, _ := cmd.Flags().GetString("stack")

	// Nixhome source: --nixhome > --local-nixhome (deprecated) > env > config.
	nixhomeSrc, _ := cmd.Flags().GetString("nixhome")
	if nixhomeSrc == "" {
		nixhomeSrc, _ = cmd.Flags().GetString("local-nixhome")
	}
	if nixhomeSrc == "" {
		nixhomeSrc = os.Getenv("DEVCELL_NIXHOME_PATH")
	}
	if nixhomeSrc == "" {
		existingCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		nixhomeSrc = existingCfg.Cell.NixhomePath
	}

	modules, _ := cmd.Flags().GetStringSlice("modules")

	// Shared init flow: resolve nixhome, pick stack/modules, scaffold.
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:    c.BaseDir,
		ConfigDir:  c.ConfigDir,
		NixhomeSrc: nixhomeSrc,
		Stack:      stack,
		Modules:    modules,
		Yes:        yes,
		Force:      force,
	})
	if err != nil {
		return err
	}

	// Update BuildDir now that .devcell.toml exists.
	c.BuildDir = config.ResolveBuildDir(c.BaseDir, c.ConfigDir, true)
	fmt.Printf(" Created .devcell.toml + .devcell/ in %s\n", c.BaseDir)
	_ = result

	// Resolve flake inputs.
	if force {
		update = true
	}
	lockOnly := !update
	label := "Resolving nix flake inputs"
	if !lockOnly {
		label = "Updating nix flake inputs"
	}
	if err := updateFlakeLockWithSpinner(c.BuildDir, lockOnly, label); err != nil {
		return err
	}

	fmt.Println(" Run 'cell build' to build the image, or 'cell claude' to build and start.")
	return nil
}
