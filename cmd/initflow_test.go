package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/scaffold"
)

// initFlowConfig is the TOML structure for reading back .devcell.toml.
type initFlowConfig struct {
	Cell struct {
		Stack   string   `toml:"stack"`
		Modules []string `toml:"modules"`
	} `toml:"cell"`
}

func readToml(t *testing.T, path string) initFlowConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var c initFlowConfig
	if _, err := toml.Decode(string(data), &c); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return c
}

// TestInitFlow_NonInteractive_DefaultsToBase verifies that -y mode
// produces a base stack with no modules and no interactive prompts.
func TestInitFlow_NonInteractive_DefaultsToBase(t *testing.T) {
	dir := t.TempDir()
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	if result.Stack != "base" {
		t.Errorf("expected stack=base, got %q", result.Stack)
	}
	if len(result.Modules) != 0 {
		t.Errorf("expected no modules, got %v", result.Modules)
	}
	// .devcell.toml should exist with stack = "base"
	c := readToml(t, filepath.Join(dir, ".devcell.toml"))
	if c.Cell.Stack != "base" {
		t.Errorf("toml stack: expected base, got %q", c.Cell.Stack)
	}
}

// TestInitFlow_ExplicitStack_SkipsPicker verifies --stack flag skips interactive.
func TestInitFlow_ExplicitStack_SkipsPicker(t *testing.T) {
	dir := t.TempDir()
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Stack:     "go",
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	if result.Stack != "go" {
		t.Errorf("expected stack=go, got %q", result.Stack)
	}
}

// TestInitFlow_CreatesAllBuildArtifacts verifies scaffold output.
func TestInitFlow_CreatesAllBuildArtifacts(t *testing.T) {
	dir := t.TempDir()
	_, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Stack:     "go",
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	for _, f := range []string{".devcell.toml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	for _, f := range []string{"Dockerfile", "flake.nix", "package.json", "pyproject.toml"} {
		if _, err := os.Stat(filepath.Join(dir, ".devcell", f)); err != nil {
			t.Errorf("missing .devcell/%s: %v", f, err)
		}
	}
}

// TestInitFlow_LocalNixhome_CopiedToBuildDir verifies local nixhome is synced.
func TestInitFlow_LocalNixhome_CopiedToBuildDir(t *testing.T) {
	dir := t.TempDir()
	// Create a fake local nixhome with a marker file.
	nixhome := filepath.Join(t.TempDir(), "nixhome")
	os.MkdirAll(filepath.Join(nixhome, "stacks"), 0755)
	os.MkdirAll(filepath.Join(nixhome, "modules"), 0755)
	os.WriteFile(filepath.Join(nixhome, "marker.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(nixhome, "stacks", "base.nix"), []byte("{ imports = [ ../modules/base.nix ]; }"), 0644)
	os.WriteFile(filepath.Join(nixhome, "modules", "base.nix"), []byte("{}"), 0644)

	_, err := RunInitFlow(InitFlowOptions{
		BaseDir:    dir,
		ConfigDir:  filepath.Join(dir, ".config", "devcell"),
		NixhomeSrc: nixhome,
		Stack:      "base",
		Yes:        true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	// Marker file should be in .devcell/nixhome/
	marker := filepath.Join(dir, ".devcell", "nixhome", "marker.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("nixhome not synced to build dir: %v", err)
	}
}

// TestInitFlow_FlakeUsesPathNixhome verifies flake.nix uses path:./nixhome
// when nixhome is present in build dir.
func TestInitFlow_FlakeUsesPathNixhome(t *testing.T) {
	dir := t.TempDir()
	// Create a fake local nixhome.
	nixhome := filepath.Join(t.TempDir(), "nixhome")
	os.MkdirAll(filepath.Join(nixhome, "stacks"), 0755)
	os.MkdirAll(filepath.Join(nixhome, "modules"), 0755)
	os.WriteFile(filepath.Join(nixhome, "stacks", "base.nix"), []byte("{ imports = [ ../modules/base.nix ]; }"), 0644)
	os.WriteFile(filepath.Join(nixhome, "modules", "base.nix"), []byte("{}"), 0644)

	_, err := RunInitFlow(InitFlowOptions{
		BaseDir:    dir,
		ConfigDir:  filepath.Join(dir, ".config", "devcell"),
		NixhomeSrc: nixhome,
		Stack:      "base",
		Yes:        true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	flake, _ := os.ReadFile(filepath.Join(dir, ".devcell", "flake.nix"))
	if !strings.Contains(string(flake), `"path:./nixhome"`) {
		t.Errorf("flake.nix should use path:./nixhome, got:\n%s", flake)
	}
}

// TestInitFlow_ReturnsBuildDir verifies the result includes the build dir path.
func TestInitFlow_ReturnsBuildDir(t *testing.T) {
	dir := t.TempDir()
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	expected := filepath.Join(dir, ".devcell")
	if result.BuildDir != expected {
		t.Errorf("expected BuildDir=%s, got %s", expected, result.BuildDir)
	}
}

// --- ResolveModuleSelection tests ---

// TestResolveModuleSelection_NoChange verifies that accepting the preset
// returns the original stack with no explicit modules.
func TestResolveModuleSelection_NoChange(t *testing.T) {
	pre := []string{"base", "build", "go", "apple", "infra", "project-management"}
	selected := []string{"base", "build", "go", "apple", "infra", "project-management"}
	stack, modules := ResolveModuleSelection("go", pre, selected)
	if stack != "go" {
		t.Errorf("expected stack=go, got %q", stack)
	}
	if modules != nil {
		t.Errorf("expected nil modules, got %v", modules)
	}
}

// TestResolveModuleSelection_AddModule verifies that adding a module
// switches to base stack with explicit module list.
func TestResolveModuleSelection_AddModule(t *testing.T) {
	pre := []string{"base", "build", "go"}
	selected := []string{"base", "build", "go", "electronics"}
	stack, modules := ResolveModuleSelection("go", pre, selected)
	if stack != "base" {
		t.Errorf("expected stack=base, got %q", stack)
	}
	expected := []string{"build", "go", "electronics"}
	if len(modules) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, modules)
	}
	for i, m := range modules {
		if m != expected[i] {
			t.Errorf("modules[%d]: expected %q, got %q", i, expected[i], m)
		}
	}
}

// TestResolveModuleSelection_RemoveModule verifies that removing a module
// switches to base stack with the remaining modules.
func TestResolveModuleSelection_RemoveModule(t *testing.T) {
	pre := []string{"base", "build", "go", "infra"}
	selected := []string{"base", "go"} // removed build, infra
	stack, modules := ResolveModuleSelection("go", pre, selected)
	if stack != "base" {
		t.Errorf("expected stack=base, got %q", stack)
	}
	if len(modules) != 1 || modules[0] != "go" {
		t.Errorf("expected [go], got %v", modules)
	}
}

// TestResolveModuleSelection_OnlyBase verifies selecting only base
// results in base stack with no extra modules.
func TestResolveModuleSelection_OnlyBase(t *testing.T) {
	pre := []string{"base", "build", "go"}
	selected := []string{"base"}
	stack, modules := ResolveModuleSelection("go", pre, selected)
	if stack != "base" {
		t.Errorf("expected stack=base, got %q", stack)
	}
	if len(modules) != 0 {
		t.Errorf("expected no modules, got %v", modules)
	}
}

// TestResolveModuleSelection_UltimateUnchanged verifies that selecting
// ultimate and not changing anything keeps stack=ultimate, modules=nil.
func TestResolveModuleSelection_UltimateUnchanged(t *testing.T) {
	pre := []string{"base", "build", "go", "apple", "infra", "node", "project-management",
		"python", "qa-tools", "scraping", "desktop", "electronics", "financial",
		"graphics", "llm", "mise", "news", "nixos", "postgresql", "security", "shell", "travel"}
	selected := make([]string, len(pre))
	copy(selected, pre)
	stack, modules := ResolveModuleSelection("ultimate", pre, selected)
	if stack != "ultimate" {
		t.Errorf("expected stack=ultimate, got %q", stack)
	}
	if modules != nil {
		t.Errorf("expected nil modules, got %v", modules)
	}
}

// TestStackModulesSubsetOfAllModules verifies that for every stack,
// the preSelected modules are a subset of allModules (the full module list).
// This is required for pterm's WithDefaultOptions to work correctly.
// Uses real nixhome files — the single source of truth.
func TestStackModulesSubsetOfAllModules_FromNixhome(t *testing.T) {
	nixhome := "/devcell-63/nixhome"
	if _, err := os.Stat(nixhome); err != nil {
		t.Skip("nixhome not available at /devcell-63/nixhome")
	}

	allModules := scanModulesFromNixhome(nixhome)
	allSet := make(map[string]bool, len(allModules))
	for _, m := range allModules {
		allSet[m] = true
	}

	stacks, _ := scanLocalStacks(nixhome)
	for _, stack := range stacks {
		preSelected := stackModulesFromNixhome(nixhome, stack)
		for _, m := range preSelected {
			if !allSet[m] {
				t.Errorf("stack %q: preSelected module %q not in scanned modules %v", stack, m, allModules)
			}
		}
		if len(preSelected) == 0 {
			t.Errorf("stack %q: preSelected is empty", stack)
		}
	}
}

// TestInitFlow_ModulesWrittenToToml verifies that when ScaffoldWithModules
// is called with explicit modules, they appear in .devcell.toml.
func TestInitFlow_ModulesWrittenToToml(t *testing.T) {
	dir := t.TempDir()

	// Simulate what happens when user customizes: stack=base + modules.
	nixhome := filepath.Join(t.TempDir(), "nixhome")
	os.MkdirAll(filepath.Join(nixhome, "stacks"), 0755)
	os.MkdirAll(filepath.Join(nixhome, "modules"), 0755)
	os.WriteFile(filepath.Join(nixhome, "stacks", "base.nix"), []byte("{ imports = [ ../modules/base.nix ]; }"), 0644)
	os.WriteFile(filepath.Join(nixhome, "modules", "base.nix"), []byte("{}"), 0644)

	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:    dir,
		ConfigDir:  filepath.Join(dir, ".config", "devcell"),
		NixhomeSrc: nixhome,
		Stack:      "base",
		Yes:        true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	// RunInitFlow with Yes=true and Stack="base" won't set modules.
	// We need to test ScaffoldWithModules directly with modules.
	_ = result

	// Now test scaffold with explicit modules.
	modules := []string{"go", "electronics"}
	err = scaffold.ScaffoldWithModules(dir, "", nixhome, true, "base", modules)
	if err != nil {
		t.Fatalf("ScaffoldWithModules: %v", err)
	}
	c := readToml(t, filepath.Join(dir, ".devcell.toml"))
	if c.Cell.Stack != "base" {
		t.Errorf("expected stack=base, got %q", c.Cell.Stack)
	}
	if len(c.Cell.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %v", c.Cell.Modules)
	}
	if c.Cell.Modules[0] != "go" || c.Cell.Modules[1] != "electronics" {
		t.Errorf("expected [go electronics], got %v", c.Cell.Modules)
	}
}

// TestInitFlow_ExplicitModules verifies --modules flag writes to .devcell.toml.
func TestInitFlow_ExplicitModules(t *testing.T) {
	dir := t.TempDir()
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Stack:     "base",
		Modules:   []string{"go", "infra", "electronics"},
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	if result.Stack != "base" {
		t.Errorf("expected stack=base, got %q", result.Stack)
	}
	if len(result.Modules) != 3 {
		t.Fatalf("expected 3 modules in result, got %v", result.Modules)
	}

	c := readToml(t, filepath.Join(dir, ".devcell.toml"))
	if c.Cell.Stack != "base" {
		t.Errorf("toml stack: expected base, got %q", c.Cell.Stack)
	}
	if len(c.Cell.Modules) != 3 {
		t.Fatalf("toml modules: expected 3, got %v", c.Cell.Modules)
	}
	for i, want := range []string{"go", "infra", "electronics"} {
		if c.Cell.Modules[i] != want {
			t.Errorf("toml modules[%d]: expected %q, got %q", i, want, c.Cell.Modules[i])
		}
	}
}

// TestInitFlow_ModulesImplyBaseStack verifies --modules without --stack defaults to base.
func TestInitFlow_ModulesImplyBaseStack(t *testing.T) {
	dir := t.TempDir()
	result, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Modules:   []string{"go", "node"},
		Yes:       true,
	})
	if err != nil {
		t.Fatalf("RunInitFlow: %v", err)
	}
	if result.Stack != "base" {
		t.Errorf("expected stack=base when modules explicit, got %q", result.Stack)
	}
}

// TestStackModulesFromNixhome_MatchesNixFiles verifies that the parsed
// module list for each stack matches the actual imports in the .nix files.
func TestStackModulesFromNixhome_MatchesNixFiles(t *testing.T) {
	nixhome := "/devcell-63/nixhome"
	if _, err := os.Stat(nixhome); err != nil {
		t.Skip("nixhome not available at /devcell-63/nixhome")
	}

	// Expected modules per stack, derived from reading the .nix files directly.
	// If a stack file changes, this test catches the drift.
	expected := map[string][]string{
		"base":        {"base"},
		"go":          {"base", "build", "go", "apple", "infra", "project-management"},
		"node":        {"base", "node", "scraping"},
		"python":      {"base", "python", "scraping"},
		"fullstack":   {"base", "build", "go", "apple", "infra", "node", "project-management", "python", "qa-tools", "scraping"},
		"electronics": {"base", "build", "desktop", "electronics"},
		"ultimate":    {"base", "build", "go", "apple", "infra", "node", "project-management", "python", "qa-tools", "scraping", "desktop", "electronics", "financial", "graphics", "llm", "mise", "news", "nixos", "postgresql", "security", "shell", "travel"},
	}

	for stack, wantModules := range expected {
		got := stackModulesFromNixhome(nixhome, stack)
		t.Logf("stack %-12s → %d modules: %v", stack, len(got), got)
		if len(got) != len(wantModules) {
			t.Errorf("stack %q: expected %d modules, got %d\n  want: %v\n  got:  %v",
				stack, len(wantModules), len(got), wantModules, got)
			continue
		}
		gotSet := make(map[string]bool, len(got))
		for _, m := range got {
			gotSet[m] = true
		}
		for _, m := range wantModules {
			if !gotSet[m] {
				t.Errorf("stack %q: missing expected module %q\n  got: %v", stack, m, got)
			}
		}
	}
}

// TestPtermReceivesCorrectPreSelected simulates the exact data flow
// that feeds into pterm's GetMultiSelection for each stack.
// Verifies: allModules contains all preSelected items (pterm precondition).
func TestPtermReceivesCorrectPreSelected(t *testing.T) {
	nixhome := "/devcell-63/nixhome"
	if _, err := os.Stat(nixhome); err != nil {
		t.Skip("nixhome not available")
	}

	stacks, _ := scanLocalStacks(nixhome)
	allModules := scanModulesFromNixhome(nixhome)
	allSet := make(map[string]bool, len(allModules))
	for _, m := range allModules {
		allSet[m] = true
	}
	t.Logf("allModules (%d): %v", len(allModules), allModules)

	for _, stack := range stacks {
		preSelected := stackModulesFromNixhome(nixhome, stack)
		t.Logf("stack %-12s preSelected (%d): %v", stack, len(preSelected), preSelected)

		if len(preSelected) == 0 {
			t.Errorf("stack %q: preSelected is EMPTY — pterm will show 0 checked", stack)
		}
		for _, m := range preSelected {
			if !allSet[m] {
				t.Errorf("stack %q: preSelected %q NOT in allModules — pterm will silently skip it", stack, m)
			}
		}
	}
}

// TestParseStackSelection_RichFormat verifies ParseStackSelection extracts
// the stack name from the rich formatted picker labels.
func TestParseStackSelection_RichFormat(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go             base, build, go, apple, infra, project-mgmt       ~3.6 GB", "go"},
		{"fullstack      base, build, go, node, python, +4 more             ~4.2 GB", "fullstack"},
		{"ultimate       all 18 modules                                     ~7.6 GB", "ultimate"},
		{"base           base                                               ~0.5 GB", "base"},
		{"go (~3.6 GB)", "go"},   // old format still works
		{"base", "base"},          // plain name
	}
	for _, tt := range tests {
		got := cfg.ParseStackSelection(tt.input)
		if got != tt.want {
			t.Errorf("ParseStackSelection(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestInitFlow_ForceOverwrites verifies --force overwrites existing files.
func TestInitFlow_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	// First run
	_, err := RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Stack:     "base",
		Yes:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Write sentinel to Dockerfile
	sentinel := "# SENTINEL\n"
	os.WriteFile(filepath.Join(dir, ".devcell", "Dockerfile"), []byte(sentinel), 0644)

	// Second run with force
	_, err = RunInitFlow(InitFlowOptions{
		BaseDir:   dir,
		ConfigDir: filepath.Join(dir, ".config", "devcell"),
		Stack:     "go",
		Yes:       true,
		Force:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".devcell", "Dockerfile"))
	if string(data) == sentinel {
		t.Error("force should overwrite existing Dockerfile")
	}
}
