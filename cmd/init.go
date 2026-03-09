package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ollama"
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
	if err := scaffold.Scaffold(c.ConfigDir, modelsSnippet); err != nil {
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
