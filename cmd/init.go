package main

import (
	"fmt"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold ~/.config/devcell/ and optionally build the image",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts and proceed with defaults")
	initCmd.Flags().Bool("macos", false, "Set up a macOS VM box via UTM + Vagrant")
}

func runInit(cmd *cobra.Command, _ []string) error {
	macos, _ := cmd.Flags().GetBool("macos")
	if macos {
		return runInitMacOS()
	}
	applyOutputFlags()
	yes, _ := cmd.Flags().GetBool("yes")

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Printf(" Scaffolding %s\n", c.ConfigDir)
	if err := scaffold.Scaffold(c.ConfigDir); err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}
	fmt.Printf(" Config dir ready: %s\n", c.ConfigDir)

	if !yes {
		ok, promptErr := ux.GetConfirmation("Build image now? (~5 min first time)")
		if promptErr != nil || !ok {
			fmt.Println(" Skipping build. Run 'cell build' when ready.")
			return nil
		}
	}

	if err := buildImageWithSpinner(c.ConfigDir, false, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}
