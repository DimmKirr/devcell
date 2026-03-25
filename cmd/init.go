package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [.]",
	Short: "Scaffold ~/.config/devcell/ (or .devcell.toml in current dir with '.')",
	RunE:  runInit,
	Args:  cobra.MaximumNArgs(1),
}

func init() {
	initCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts and proceed with defaults")
	initCmd.Flags().Bool("macos", false, "Set up a macOS VM box via UTM + Vagrant")
	initCmd.Flags().Bool("force", false, "Overwrite existing files and update flake inputs (implies --update)")
	initCmd.Flags().Bool("update", false, "update nix flake inputs (pull latest) instead of just resolving")
	initCmd.Flags().String("local-nixhome", "", "path to local nixhome to copy and use (generates path:./nixhome flake input)")
}

func runInit(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && args[0] == "." {
		return runInitProject(cmd)
	}
	macos, _ := cmd.Flags().GetBool("macos")
	if macos {
		return runInitMacOS()
	}
	applyOutputFlags()
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	update, _ := cmd.Flags().GetBool("update")

	// Override base image tag for scaffold Dockerfile if --base-image is set.
	if bi, _ := cmd.Flags().GetString("base-image"); bi != "" {
		os.Setenv("DEVCELL_BASE_IMAGE", bi)
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Detect ollama and generate commented-out models snippet for devcell.toml.
	// If ollama is not reachable, modelsSnippet is "" and the default example is used.
	modelsSnippet := detectOllamaModels()

	fmt.Printf(" Scaffolding %s\n", c.ConfigDir)
	nixhomePath, _ := cmd.Flags().GetString("local-nixhome")
	if nixhomePath == "" {
		nixhomePath = os.Getenv("DEVCELL_NIXHOME_PATH")
	}
	if err := scaffold.Scaffold(c.ConfigDir, modelsSnippet, nixhomePath, force); err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}
	// Copy local nixhome into config dir when --local-nixhome is set.
	if nixhomePath != "" {
		if err := scaffold.SyncNixhome(nixhomePath, c.ConfigDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}
	fmt.Printf(" Config dir ready: %s\n", c.ConfigDir)

	// Resolve flake inputs (generates flake.lock if missing).
	// --update or --force pulls latest instead of just resolving.
	if force {
		update = true
	}
	lockOnly := !update
	label := "Resolving nix flake inputs"
	if !lockOnly {
		label = "Updating nix flake inputs"
	}
	if err := updateFlakeLockWithSpinner(c.ConfigDir, lockOnly, label); err != nil {
		return err
	}

	if !yes {
		ok, promptErr := ux.GetConfirmation("Build image now? (~5 min first time)")
		if promptErr != nil || !ok {
			fmt.Println(" Skipping build. Run 'cell build' when ready.")
			return nil
		}
	}

	if err := buildImageWithSpinner(c.ConfigDir, force, "Building devcell image", false); err != nil {
		return err
	}
	return nil
}

// runInitProject handles `cell init .` — creates a .devcell.toml in the current directory.
func runInitProject(_ *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ok, err := ux.GetConfirmation(fmt.Sprintf("Create %s/.devcell.toml?", cwd))
	if err != nil || !ok {
		return nil
	}
	if err := scaffold.ScaffoldProject(cwd); err != nil {
		if errors.Is(err, os.ErrExist) {
			overwrite, promptErr := ux.GetConfirmation(".devcell.toml already exists. Overwrite?")
			if promptErr != nil || !overwrite {
				return nil
			}
			if err := scaffold.ScaffoldProjectForce(cwd); err != nil {
				return err
			}
			fmt.Println(" Overwrote .devcell.toml")
			return nil
		}
		return err
	}
	fmt.Println(" Created .devcell.toml")
	return nil
}

// detectOllamaModels tries to detect ollama and returns a commented-out
// TOML snippet for devcell.toml. Returns "" if ollama is not reachable.
func detectOllamaModels() string {
	ctx := context.Background()
	if !ollama.Detect(ctx, ollama.DefaultBaseURL) {
		return ""
	}
	models, err := ollama.FetchModels(ctx, ollama.DefaultBaseURL)
	if err != nil || len(models) == 0 {
		return ""
	}
	ranked := ollama.RankModels(models, 10, nil, nil)
	snippet := ollama.FormatActiveTOMLSnippet(ranked)
	if snippet != "" {
		fmt.Printf(" Detected ollama with %d models\n", len(ranked))
	}
	return snippet
}
