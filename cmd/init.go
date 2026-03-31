package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
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
	initCmd.Flags().String("local-nixhome", "", "path to local nixhome to copy and use (generates path:./nixhome flake input)")
	initCmd.Flags().String("stack", "", "stack name (base, go, node, python, fullstack, electronics, ultimate)")
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

	// Override base image tag for scaffold Dockerfile if --base-image is set.
	if bi, _ := cmd.Flags().GetString("base-image"); bi != "" {
		os.Setenv("DEVCELL_BASE_IMAGE", bi)
	}

	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Determine stack: --stack flag > interactive picker > "base" (with -y)
	stack, _ := cmd.Flags().GetString("stack")

	// Nixhome path: --local-nixhome flag > DEVCELL_NIXHOME_PATH env > existing config nixhome field.
	// At init time, .devcell.toml may not exist yet, so env/flag take priority.
	nixhomePath, _ := cmd.Flags().GetString("local-nixhome")
	if nixhomePath == "" {
		nixhomePath = os.Getenv("DEVCELL_NIXHOME_PATH")
	}
	if nixhomePath == "" {
		// Check existing global config for nixhome path.
		existingCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
		nixhomePath = existingCfg.Cell.NixhomePath
	}

	if stack == "" && !yes {
		// Local nixhome: scan filesystem for custom stacks.
		// Otherwise: use hardcoded list (no Docker pull needed for the picker).
		stacks := cfg.KnownStacksWithSizes()
		source := "built-in"
		if nixhomePath != "" {
			if local, err := scanLocalStacks(nixhomePath); err == nil && len(local) > 0 {
				stacks = local
				source = nixhomePath + "/stacks/*.nix"
			}
		}
		ux.Debugf("Stack list (%s): %v", source, stacks)
		picked, selErr := ux.GetSelection("Pick a stack", stacks)
		if selErr != nil {
			return fmt.Errorf("stack selection: %w", selErr)
		}
		stack = cfg.ParseStackSelection(picked)
	}
	if stack == "" {
		stack = "base" // -y mode default: smallest image, fastest first build
	}

	// Detect ollama and generate commented-out models snippet for .devcell.toml.
	modelsSnippet := detectOllamaModels()

	// Scaffold .devcell.toml + .devcell/ in project dir (cwd).
	ux.Debugf("BuildDir: %s", c.BuildDir)
	fmt.Printf(" Initializing %s\n", c.BaseDir)
	if err := scaffold.Scaffold(c.BaseDir, modelsSnippet, nixhomePath, force, stack); err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}

	// Update BuildDir now that .devcell.toml exists.
	c.BuildDir = config.ResolveBuildDir(c.BaseDir, c.ConfigDir, true)
	ux.Debugf("BuildDir: %s", c.BuildDir)

	// Sync local nixhome into build context when nixhome path is set.
	if nixhomePath != "" {
		ux.Debugf("Syncing nixhome: %s → %s/nixhome/", nixhomePath, c.BuildDir)
		if err := scaffold.SyncNixhome(nixhomePath, c.BuildDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}
	fmt.Printf(" Created .devcell.toml + .devcell/ in %s\n", c.BaseDir)

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
	if err := updateFlakeLockWithSpinner(c.BuildDir, lockOnly, label); err != nil {
		return err
	}

	fmt.Println(" Run 'cell build' to build the image, or 'cell claude' to build and start.")
	return nil
}

// scanLocalStacks lists stack names from a local nixhome directory.
func scanLocalStacks(nixhomePath string) ([]string, error) {
	entries, err := filepath.Glob(filepath.Join(nixhomePath, "stacks", "*.nix"))
	if err != nil {
		return nil, err
	}
	var stacks []string
	for _, e := range entries {
		name := strings.TrimSuffix(filepath.Base(e), ".nix")
		if name != "" {
			stacks = append(stacks, name)
		}
	}
	sort.Strings(stacks)
	return stacks, nil
}

// detectOllamaModels tries to detect ollama and returns a commented-out
// TOML snippet for .devcell.toml. Returns "" if ollama is not reachable.
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
