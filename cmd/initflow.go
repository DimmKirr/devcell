package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
)

// InitFlowOptions configures the shared initialization flow.
type InitFlowOptions struct {
	BaseDir    string   // project root directory
	ConfigDir  string   // global config directory (~/.config/devcell)
	NixhomeSrc string   // nixhome source: local path, git URL, or "" for upstream
	Stack      string   // explicit stack name (skips picker if set)
	Modules    []string // explicit modules (skips multiselect if set)
	Yes        bool     // skip interactive prompts, use defaults
	Force      bool     // overwrite existing files
}

// InitFlowResult holds the output of a successful init flow.
type InitFlowResult struct {
	Stack    string
	Modules  []string
	BuildDir string
}

// RunInitFlow is the shared init logic used by both `cell init` and `cell claude` first-run.
// It resolves nixhome, runs the stack/module picker (unless non-interactive),
// and scaffolds the project.
func RunInitFlow(opts InitFlowOptions) (*InitFlowResult, error) {
	buildDir := filepath.Join(opts.BaseDir, ".devcell")

	// Check if already initialized — ask to overwrite unless -y or --force.
	if !opts.Force && !opts.Yes {
		if _, err := os.Stat(filepath.Join(opts.BaseDir, ".devcell.toml")); err == nil {
			overwrite, cErr := ux.GetConfirmation("Project already initialized. Re-initialize?")
			if cErr != nil {
				return nil, fmt.Errorf("confirmation: %w", cErr)
			}
			if !overwrite {
				return nil, fmt.Errorf("cancelled")
			}
			opts.Force = true
		}
	}

	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", buildDir, err)
	}

	// Resolve nixhome into .devcell/nixhome/.
	if err := scaffold.ResolveNixhome(opts.NixhomeSrc, buildDir, version.Version, opts.Force); err != nil {
		ux.Debugf("Failed to resolve nixhome: %v (falling back to built-in lists)", err)
	}
	if err := validateNixhomeStructure(filepath.Join(buildDir, "nixhome")); err != nil {
		return nil, err
	}

	// For scaffold: pass nixhomeSrc only if it's a local path (persisted in .devcell.toml).
	nixhomePath := ""
	if opts.NixhomeSrc != "" && !scaffold.IsGitURL(opts.NixhomeSrc) {
		nixhomePath = opts.NixhomeSrc
	}

	nixhomeDest := filepath.Join(buildDir, "nixhome")
	stack := opts.Stack
	modules := opts.Modules

	if len(modules) > 0 && stack == "" {
		stack = "base" // explicit modules imply base stack
	}

	if stack == "" && !opts.Yes {
		stackOpts, source := scanStacksFromNixhome(nixhomeDest)
		ux.Debugf("Stack list (%s): %d stacks", source, len(stackOpts))

		// Loop: stack picker → module multiselect.
		// Esc in multiselect returns to stack picker.
		for {
			picked, selErr := ux.GetSelectionKV("Pick a stack", stackOpts)
			if selErr != nil {
				return nil, fmt.Errorf("stack selection: %w", selErr)
			}
			stack = picked

			allModules := scanModulesFromNixhome(nixhomeDest)
			preSelected := stackModulesFromNixhome(nixhomeDest, stack)
			ux.Debugf("allModules (%d): %v", len(allModules), allModules)
			ux.Debugf("preSelected (%d): %v", len(preSelected), preSelected)
			selected, msErr := ux.GetMultiSelection(
				"Modules (space: toggle, enter: confirm, esc: back)",
				allModules, preSelected,
			)
			if msErr == ux.ErrUserAborted {
				// Esc → clear and go back to stack picker.
				fmt.Print("\033[2J\033[H") // clear screen, cursor to top
				stack = ""
				continue
			}
			if msErr != nil {
				return nil, fmt.Errorf("module selection: %w", msErr)
			}

			stack, modules = ResolveModuleSelection(stack, preSelected, selected)
			break
		}
	}
	if stack == "" {
		stack = "base"
	}

	// Detect ollama models.
	modelsSnippet := detectOllamaModels()

	// Scaffold.
	fmt.Printf(" Initializing %s\n", opts.BaseDir)
	if err := scaffold.ScaffoldWithModules(opts.BaseDir, modelsSnippet, nixhomePath, opts.Force, stack, modules); err != nil {
		return nil, fmt.Errorf("scaffold: %w", err)
	}

	return &InitFlowResult{
		Stack:    stack,
		Modules:  modules,
		BuildDir: buildDir,
	}, nil
}

// ResolveModuleSelection computes the effective stack and modules from the
// user's multiselect choices. If the selection matches the stack preset
// exactly, stack is unchanged and modules is nil. If the user added or removed
// modules, stack becomes "base" and modules lists the non-base selections.
func ResolveModuleSelection(stack string, preSelected, selected []string) (string, []string) {
	preSet := make(map[string]bool, len(preSelected))
	for _, m := range preSelected {
		preSet[m] = true
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, m := range selected {
		selectedSet[m] = true
	}

	changed := len(selected) != len(preSelected)
	if !changed {
		for _, m := range selected {
			if !preSet[m] {
				changed = true
				break
			}
		}
	}
	if !changed {
		return stack, nil
	}

	// User customized — use base stack + explicit module list.
	var modules []string
	for _, m := range selected {
		if m != "base" {
			modules = append(modules, m)
		}
	}
	return "base", modules
}

// --- Helpers used by RunInitFlow (moved from init.go) ---

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

// scanLocalModules lists module names from a local nixhome directory.
func scanLocalModules(nixhomePath string) ([]string, error) {
	modDir := filepath.Join(nixhomePath, "modules")
	entries, err := os.ReadDir(modDir)
	if err != nil {
		return nil, err
	}
	var modules []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name != "fragments" {
				modules = append(modules, name)
			}
		} else if strings.HasSuffix(name, ".nix") {
			modules = append(modules, strings.TrimSuffix(name, ".nix"))
		}
	}
	sort.Strings(modules)
	return modules, nil
}

// validateNixhomeStructure checks that the nixhome directory has the expected
// stacks/ and modules/ subdirectories. Returns an error if the structure is
// incompatible with devcell (no stacks/ or no modules/).
func validateNixhomeStructure(nixhomePath string) error {
	if nixhomePath == "" {
		return nil
	}
	if _, err := os.Stat(nixhomePath); err != nil {
		return nil // nixhome not fetched — will use defaults
	}
	var missing []string
	if _, err := os.Stat(filepath.Join(nixhomePath, "stacks")); err != nil {
		missing = append(missing, "stacks/")
	}
	if _, err := os.Stat(filepath.Join(nixhomePath, "modules")); err != nil {
		missing = append(missing, "modules/")
	}
	if len(missing) > 0 {
		return fmt.Errorf("nixhome at %s is not devcell-compatible (missing %s). Expected stacks/*.nix and modules/*.nix",
			nixhomePath, strings.Join(missing, ", "))
	}
	return nil
}

// scanStacksFromNixhome scans .devcell/nixhome/ for stacks.
// Falls back to KnownStacks if nixhome isn't available.
// Returns SelectOption with Label (display) and Value (stack name).
func scanStacksFromNixhome(nixhomePath string) ([]ux.SelectOption, string) {
	if stacks, err := scanLocalStacks(nixhomePath); err == nil && len(stacks) > 0 {
		opts := make([]ux.SelectOption, 0, len(stacks))
		for _, s := range stacks {
			mods := stackModulesFromNixhome(nixhomePath, s)
			modStr := strings.Join(mods, ", ")
			if len(mods) > 6 {
				modStr = strings.Join(mods[:6], ", ") + fmt.Sprintf(", +%d more", len(mods)-6)
			}
			sz := ""
			if szVal, ok := cfg.StackSize(s); ok {
				sz = szVal
			}
			label := fmt.Sprintf("%-14s %-52s %s", s, modStr, sz)
			opts = append(opts, ux.SelectOption{Label: label, Value: s})
		}
		return opts, nixhomePath + "/stacks/*.nix"
	}
	// No nixhome on disk — fall back to known stack names with sizes.
	known := cfg.KnownStacks()
	opts := make([]ux.SelectOption, len(known))
	for i, s := range known {
		label := s
		if sz, ok := cfg.StackSize(s); ok {
			label = fmt.Sprintf("%s (%s)", s, sz)
		}
		opts[i] = ux.SelectOption{Label: label, Value: s}
	}
	return opts, "built-in (nixhome not available)"
}

// scanModulesFromNixhome scans .devcell/nixhome/modules/ for available modules.
// Returns nil if nixhome isn't available.
func scanModulesFromNixhome(nixhomePath string) []string {
	mods, _ := scanLocalModules(nixhomePath)
	return mods
}

// stackModulesFromNixhome reads a stack .nix file and extracts its module imports.
// Returns nil if the stack file doesn't exist.
func stackModulesFromNixhome(nixhomePath, stack string) []string {
	stackFile := filepath.Join(nixhomePath, "stacks", stack+".nix")
	data, err := os.ReadFile(stackFile)
	if err != nil {
		return nil
	}
	return parseStackImports(nixhomePath, string(data))
}

// parseStackImports extracts module names from nix import paths.
// Recursively follows ./other-stack.nix imports.
func parseStackImports(nixhomePath, content string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "../modules/") {
			part := line
			if i := strings.Index(part, "../modules/"); i >= 0 {
				part = part[i+len("../modules/"):]
			}
			part = strings.TrimRight(part, " \t;]}")
			part = strings.TrimSuffix(part, ".nix")
			if part != "" && !seen[part] {
				seen[part] = true
				result = append(result, part)
			}
		}
		if strings.Contains(line, "./") && strings.HasSuffix(strings.TrimRight(line, " \t;]}"), ".nix") && !strings.Contains(line, "../") {
			part := line
			if i := strings.Index(part, "./"); i >= 0 {
				part = part[i:]
			}
			part = strings.TrimRight(part, " \t;]}")
			refFile := filepath.Join(nixhomePath, "stacks", part)
			if data, err := os.ReadFile(refFile); err == nil {
				for _, m := range parseStackImports(nixhomePath, string(data)) {
					if !seen[m] {
						seen[m] = true
						result = append(result, m)
					}
				}
			}
		}
	}
	return result
}

// detectOllamaModels tries to detect ollama and returns a commented-out
// TOML snippet for .devcell.toml.
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
