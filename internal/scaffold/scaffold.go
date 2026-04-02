package scaffold

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/DimmKirr/devcell/internal/version"
)

//go:embed templates/devcell.toml.tmpl
var devcellTomlContent []byte

//go:embed templates/starship.toml.tmpl
var starshipTomlContent []byte

//go:embed templates/Vagrantfile.tmpl
var vagrantfileContent []byte

type scaffoldFile struct {
	name    string
	content []byte
}

// defaultModelsSection is the generic commented example used when no
// ollama models are detected.
const defaultModelsSection = `# [llm.models]
# Default LLM model (format: provider/model). Used by opencode and other agents.
# default = "ollama/deepseek-r1:32b"

# [llm.models.providers.ollama]
# models = ["deepseek-r1:32b", "qwen3:8b"]

# [llm.models.providers.lmstudio]
# base_url = "http://host.docker.internal:1234/v1"
# models = ["deepseek-r1:32b"]`

func scaffoldFiles(modelsSnippet string, withNixhome bool, stack string, modules []string) []scaffoldFile {
	dockerfile := []byte(GenerateDockerfileWithNixhome("", withNixhome, stack, modules))
	flake := []byte(GenerateFlakeNix(stack, modules, version.Version, withNixhome))

	models := modelsSnippet
	if models == "" {
		models = defaultModelsSection
	}
	tomlContent := bytes.ReplaceAll(devcellTomlContent, []byte("{{MODELS_SECTION}}"), []byte(models))

	if stack != "" {
		tomlContent = bytes.ReplaceAll(tomlContent,
			[]byte(`# stack = "base"`),
			[]byte(fmt.Sprintf(`stack = %q`, stack)))
	}
	if len(modules) > 0 {
		// Format modules as TOML array: modules = ["go", "infra"]
		quoted := make([]string, len(modules))
		for i, m := range modules {
			quoted[i] = fmt.Sprintf("%q", m)
		}
		modulesLine := fmt.Sprintf("modules = [%s]", strings.Join(quoted, ", "))
		// Replace the commented example line in the template.
		tomlContent = bytes.ReplaceAll(tomlContent,
			[]byte(`# modules = ["electronics", "desktop"]`),
			[]byte(modulesLine))
	}

	return []scaffoldFile{
		{"Dockerfile", dockerfile},
		{"flake.nix", flake},
		{"devcell.toml", tomlContent},
	}
}

// generatePackageJSON builds package.json from [packages.npm] config.
func generatePackageJSON(pkgs map[string]string) []byte {
	deps := make(map[string]string, len(pkgs))
	for k, v := range pkgs {
		deps[k] = v
	}
	obj := map[string]any{
		"name":         "devcell-tools",
		"version":      "1.0.0",
		"private":      true,
		"dependencies": deps,
	}
	data, _ := json.MarshalIndent(obj, "", "  ")
	return append(data, '\n')
}

// generatePyprojectTOML builds pyproject.toml from [packages.python] config.
func generatePyprojectTOML(pkgs map[string]string) []byte {
	var deps []string
	for name, ver := range pkgs {
		if ver == "*" || ver == "" {
			deps = append(deps, fmt.Sprintf("    %q,", name))
		} else {
			deps = append(deps, fmt.Sprintf("    %q,", name+"=="+ver))
		}
	}
	sort.Strings(deps)

	var b strings.Builder
	b.WriteString("[project]\n")
	b.WriteString("name = \"devcell-tools\"\n")
	b.WriteString("version = \"1.0.0\"\n")
	b.WriteString("requires-python = \">=3.13\"\n")
	b.WriteString("dependencies = [\n")
	for _, d := range deps {
		b.WriteString(d + "\n")
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

// GenerateFlakeNix produces a flake.nix string that imports the given stack
// and modules from the upstream devcell nixhome flake.
// stack is a stack name (e.g. "go"), modules is a list of module names,
// ver is the version tag, nixhomePath overrides the input URL to path:./nixhome.
func GenerateFlakeNix(stack string, modules []string, ver string, withNixhome bool) string {
	if stack == "" {
		stack = "base"
	}
	inputURL := fmt.Sprintf(`"github:DimmKirr/devcell/%s?dir=nixhome"`, ver)
	if withNixhome {
		inputURL = `"path:./nixhome"`
	}

	// Build the module expression for nix. devcell.stacks.X is already a list,
	// so we concatenate with ++ rather than wrapping in [...].
	moduleExpr := fmt.Sprintf("devcell.stacks.%s", stack)
	for _, m := range modules {
		moduleExpr += fmt.Sprintf(" ++ devcell.modules.%s", m)
	}

	return fmt.Sprintf(`{
  description = "DevCell user stack — customise and run 'cell build'";

  # Follows main branch by default. To pin a specific release:
  #   inputs.devcell.url = "github:DimmKirr/devcell/v1.0.0?dir=nixhome";
  # To use your own nixhome fork:
  #   inputs.devcell.url = "github:yourusername/nixhome";
  inputs.devcell.url = %s;

  outputs = { self, devcell, ... }: {
    homeConfigurations = {
      "devcell-local" = devcell.lib.mkHome "x86_64-linux" (%s);
      "devcell-local-aarch64" = devcell.lib.mkHome "aarch64-linux" (%s);
    };
  };
}
`, inputURL, moduleExpr, moduleExpr)
}

// GenerateDockerfile produces a Dockerfile string for the .devcell/ build context.
// baseImage overrides the FROM line; empty uses runner.BaseImageTag().
func GenerateDockerfile(baseImage string) string {
	return GenerateDockerfileWithNixhome(baseImage, false, "base", nil)
}

// GenerateDockerfileWithNixhome produces a Dockerfile with optional nixhome COPY.
// stack and modules are embedded as ARG defaults for /etc/devcell/metadata.json.
func GenerateDockerfileWithNixhome(baseImage string, withNixhome bool, stack string, modules []string) string {
	if baseImage == "" {
		baseImage = runner.BaseImageTag()
	}
	if stack == "" {
		stack = "base"
	}

	modulesStr := strings.Join(modules, ",")

	var nixhomeCopy string
	if withNixhome {
		nixhomeCopy = "COPY --chown=devcell:usergroup nixhome/ /opt/devcell/.config/devcell/nixhome/\n"
	}

	return fmt.Sprintf(`FROM %s

# Build metadata — propagated to nix activation script (base.nix writeMetadata).
ARG GIT_COMMIT=unknown
ARG DEVCELL_BASE_IMAGE="%s"
ARG DEVCELL_STACK="%s"
ARG DEVCELL_MODULES="%s"

# Copy flake + lock. The glob (flake.*) makes flake.lock optional — first build
# won't have one yet; nix creates it and subsequent builds reuse it, pinning
# inputs so the base image's /nix/store paths are found without re-downloading.
%sCOPY --chown=devcell:usergroup flake.* /opt/devcell/.config/devcell/

# Activate the nix profile.
# NIX_REFRESH is set to "--refresh" by `+"`cell build --no-cache`"+` to bust nix flake cache.
ARG NIX_REFRESH=""
RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch \
      --flake "/opt/devcell/.config/devcell#devcell-local${ARCH_SUFFIX}" \
      --impure $NIX_REFRESH && \
    { nix-collect-garbage -d; nix-store --optimise; true; }

# Install language runtimes via mise (separate layer — conditional on stack having mise).
RUN which mise && \
    (mkdir -p /opt/mise 2>/dev/null || sudo mkdir -p /opt/mise) && \
    cd /opt/devcell && MISE_DATA_DIR=/opt/mise MISE_YES=1 mise install && \
    for tool_dir in /opt/mise/installs/*/; do \
      tool=$(basename "$tool_dir"); \
      version_dir=$(ls -1d "${tool_dir}"*/ 2>/dev/null | head -1); \
      if [ -n "$version_dir" ]; then ln -sfT "$version_dir" "/opt/mise/$tool"; fi; \
    done || true

# Add mise-installed tool bins to PATH via stable symlinks
ENV PATH="/opt/mise/node/bin:/opt/mise/go/bin:${PATH}"

# Agent CLI tools — conditional on stack having npm
COPY --chown=devcell:usergroup package.json /opt/npm-tools/
RUN which npm && cd /opt/npm-tools && npm install || true
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"

# Python tools — conditional on stack having uv
COPY --chown=devcell:usergroup pyproject.toml /opt/python-tools/
SHELL ["/bin/bash", "-c"]
RUN which uv && cd /opt/python-tools && uv sync || true
SHELL ["/bin/sh", "-c"]
ENV PATH="/opt/python-tools/.venv/bin:${PATH}"
`, baseImage, baseImage, stack, modulesStr, nixhomeCopy)
}

// Scaffold writes scaffold files to dir, then generates package.json and
// pyproject.toml from the [packages] section in devcell.toml.
// Files that already exist are skipped (idempotent) unless force is true.
// modelsSnippet is an optional commented-out [models] section for devcell.toml;
// pass "" to use the default generic example.

const defaultNixhomeRepo = "https://github.com/DimmKirr/devcell.git"

// IsGitURL returns true if source looks like a git URL or GitHub shorthand.
func IsGitURL(source string) bool {
	return strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "github:") ||
		strings.HasPrefix(source, "ssh://")
}

// ResolveNixhome pulls nixhome into buildDir/nixhome/ from the given source.
//   - Local path: copy directly (rsync-like)
//   - Git URL: shallow sparse clone, extract nixhome/ subdir
//   - Empty source: clone from upstream repo at the given version tag
//
// Skips if buildDir/nixhome/ already exists and force is false.
func ResolveNixhome(source, buildDir, ver string, force bool) error {
	dest := filepath.Join(buildDir, "nixhome")

	// Skip if already present and not forcing.
	if !force {
		if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
			return nil
		}
	}

	if source != "" && !IsGitURL(source) {
		// Local path — copy.
		return SyncNixhome(source, buildDir)
	}

	// Git source — resolve URL.
	repoURL := defaultNixhomeRepo
	subdir := "nixhome"
	if source != "" {
		repoURL = normalizeGitURL(source)
		// If the URL doesn't contain "devcell", assume it points directly to a
		// nixhome repo (no subdirectory needed).
		if !strings.Contains(repoURL, "devcell") {
			subdir = ""
		}
	}

	ref := ver
	if ref == "" || ref == "v0.0.0" {
		ref = "main"
	}

	sp := ux.NewProgressSpinner("Fetching nixhome from git")

	tmpDir, err := os.MkdirTemp("", "devcell-nixhome-*")
	if err != nil {
		sp.Fail("Fetch nixhome failed")
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Shallow clone with sparse checkout.
	cloneArgs := []string{"clone", "--depth", "1", "--branch", ref, "--filter=blob:none"}
	if subdir != "" {
		cloneArgs = append(cloneArgs, "--sparse")
	}
	cloneArgs = append(cloneArgs, repoURL, tmpDir)

	cmds := [][]string{{"git"}}
	cmds[0] = append(cmds[0], cloneArgs...)
	if subdir != "" {
		cmds = append(cmds, []string{"git", "-C", tmpDir, "sparse-checkout", "set", subdir})
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Run(); err != nil {
			sp.Fail("Fetch nixhome failed")
			return fmt.Errorf("%s: %w", strings.Join(args[:2], " "), err)
		}
	}

	// Copy from clone to dest.
	src := tmpDir
	if subdir != "" {
		src = filepath.Join(tmpDir, subdir)
	}
	if _, err := os.Stat(src); err != nil {
		sp.Fail("Fetch nixhome failed")
		return fmt.Errorf("nixhome not found in clone: %w", err)
	}
	os.RemoveAll(dest)
	os.Remove(filepath.Join(buildDir, "flake.lock"))
	if err := CopyDir(src, dest); err != nil {
		sp.Fail("Fetch nixhome failed")
		return err
	}

	sp.Success(fmt.Sprintf("Fetched nixhome (%s)", ref))
	return nil
}

// normalizeGitURL converts shorthand like "github:user/repo" to a full URL.
func normalizeGitURL(source string) string {
	if strings.HasPrefix(source, "github:") {
		return "https://github.com/" + strings.TrimPrefix(source, "github:") + ".git"
	}
	return source
}

// SyncNixhome copies the nixhome directory from srcPath into configDir/nixhome/.
// It replaces any existing nixhome copy to ensure fresh content each build.
// Also removes the outer flake.lock so nix regenerates it from the inner
// nixhome's inputs — prevents stale lock from pinning different nixpkgs
// than the base image, which would cause a full re-download.
func SyncNixhome(srcPath, configDir string) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("nixhome source %s: %w", srcPath, err)
	}
	dest := filepath.Join(configDir, "nixhome")
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove old nixhome: %w", err)
	}
	os.Remove(filepath.Join(configDir, "flake.lock"))
	return CopyDir(srcPath, dest)
}

// CopyDir recursively copies src directory to dst.
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// Scaffold writes .devcell.toml to dir (project root) and build artifacts
// (Dockerfile, flake.nix, package.json, pyproject.toml, starship.toml) to
// dir/.devcell/ (build context, gitignored).
// ScaffoldWithModules is like Scaffold but also writes the selected modules list.
func ScaffoldWithModules(dir string, modelsSnippet string, nixhomePath string, force bool, stack string, modules []string) error {
	return doScaffold(dir, modelsSnippet, nixhomePath, force, stack, modules)
}

func Scaffold(dir string, modelsSnippet string, nixhomePath string, force bool, stack ...string) error {
	stk := ""
	if len(stack) > 0 {
		stk = stack[0]
	}
	return doScaffold(dir, modelsSnippet, nixhomePath, force, stk, nil)
}

func doScaffold(dir string, modelsSnippet string, nixhomePath string, force bool, stk string, modules []string) error {

	buildDir := filepath.Join(dir, ".devcell")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", buildDir, err)
	}

	// Sync nixhome FIRST so the detect-on-disk check below sees it.
	if nixhomePath != "" {
		if err := SyncNixhome(nixhomePath, buildDir); err != nil {
			return fmt.Errorf("sync nixhome: %w", err)
		}
	}

	// Detect nixhome on disk — if .devcell/nixhome/ exists, use path:./nixhome.
	_, nixhomeStat := os.Stat(filepath.Join(buildDir, "nixhome"))
	withNixhome := nixhomeStat == nil

	// Write .devcell.toml to project root, build artifacts to .devcell/.
	for _, f := range scaffoldFiles(modelsSnippet, withNixhome, stk, modules) {
		var dest string
		if f.name == "devcell.toml" {
			// Config file → project root as .devcell.toml (dot-prefixed)
			dest = filepath.Join(dir, ".devcell.toml")
		} else {
			// Build artifacts → .devcell/ subdir
			dest = filepath.Join(buildDir, f.name)
		}
		if !force {
			if _, err := os.Stat(dest); err == nil {
				continue
			}
		}
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	// Scaffold homedir/.config/starship.toml for per-project prompt customization.
	starshipDir := filepath.Join(buildDir, "homedir", ".config")
	starshipDest := filepath.Join(starshipDir, "starship.toml")
	if force || os.IsNotExist(statErr(starshipDest)) {
		if err := os.MkdirAll(starshipDir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", starshipDir, err)
		}
		if err := os.WriteFile(starshipDest, starshipTomlContent, 0644); err != nil {
			return fmt.Errorf("write homedir starship.toml: %w", err)
		}
	}

	// Generate package files from .devcell.toml [packages] config.
	c, err := cfg.LoadFile(filepath.Join(dir, ".devcell.toml"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	generated := []scaffoldFile{
		{"package.json", generatePackageJSON(c.Packages.Npm)},
		{"pyproject.toml", generatePyprojectTOML(c.Packages.Python)},
	}
	for _, f := range generated {
		dest := filepath.Join(buildDir, f.name)
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// RegenerateBuildContext regenerates all build artifacts (flake.nix, Dockerfile,
// package.json, pyproject.toml) from the merged config. Call before every build
// so that changes to stack/modules in devcell.toml take effect without re-running
// cell init.
//
// Cache optimization: when the user picks a known stack, we try to use the
// pre-built stack image (ghcr.io/dimmkirr/devcell:latest-<stack>) as the FROM
// line. This lets Docker/nix reuse the existing /nix/store paths from that
// image — only the delta is downloaded. If the pre-built image isn't available
// (not yet pushed, network error), we fall back to the core image.
func RegenerateBuildContext(configDir string, cellCfg cfg.CellConfig) error {
	// Detect nixhome on disk — if .devcell/nixhome/ exists, use path:./nixhome.
	_, statErr := os.Stat(filepath.Join(configDir, "nixhome"))
	withNixhome := statErr == nil

	stack := cellCfg.Cell.ResolvedStack()

	// Regenerate flake.nix from stack + modules.
	flake := GenerateFlakeNix(stack, cellCfg.Cell.Modules, version.Version, withNixhome)
	if err := os.WriteFile(filepath.Join(configDir, "flake.nix"), []byte(flake), 0644); err != nil {
		return fmt.Errorf("write flake.nix: %w", err)
	}

	// Determine the best FROM image for nix cache reuse.
	baseImage := resolveBaseImage(stack)

	// Regenerate Dockerfile.
	df := GenerateDockerfileWithNixhome(baseImage, withNixhome, stack, cellCfg.Cell.Modules)
	if err := os.WriteFile(filepath.Join(configDir, "Dockerfile"), []byte(df), 0644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}

	// Regenerate package files.
	generated := []scaffoldFile{
		{"package.json", generatePackageJSON(cellCfg.Packages.Npm)},
		{"pyproject.toml", generatePyprojectTOML(cellCfg.Packages.Python)},
	}
	for _, f := range generated {
		dest := filepath.Join(configDir, f.name)
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// resolveBaseImage picks the best FROM image for the Dockerfile.
// Priority:
//  1. DEVCELL_BASE_IMAGE env var (explicit override — local dev, CI)
//  2. Pre-built stack image from registry (nix cache reuse)
//  3. Default core image (fallback)
func resolveBaseImage(stack string) string {
	// Explicit override wins — user knows what they want.
	if tag := os.Getenv("DEVCELL_BASE_IMAGE"); tag != "" {
		if stack != "base" && cfg.ValidateStack(stack) == nil {
			ux.Debugf("Stack cache candidate: %s (skipped — DEVCELL_BASE_IMAGE override)", runner.StackImageTag(stack))
		}
		ux.Debugf("FROM image: %s (DEVCELL_BASE_IMAGE override)", tag)
		return tag
	}

	// Try pre-built stack image for nix store cache reuse.
	// "base" stack doesn't benefit — it's tiny and core already has nix.
	if stack != "base" && cfg.ValidateStack(stack) == nil {
		stackTag := runner.StackImageTag(stack)

		// Check local first, then try pull.
		ctx := context.Background()
		if runner.ImageExists(ctx, stackTag) {
			ux.Debugf("FROM image: %s (local pre-built stack cache)", stackTag)
			return stackTag
		}

		label := fmt.Sprintf("Pulling stack cache image %s", stackTag)
		sp := ux.NewProgressSpinner(label)
		if err := runner.PullImage(ctx, stackTag); err == nil {
			sp.Success(label)
			ux.Debugf("FROM image: %s (pulled pre-built stack cache)", stackTag)
			return stackTag
		}
		sp.Stop()
		ux.Debugf("Pre-built stack image not available, falling back to core")
	}

	// Default: core image.
	tag := runner.BaseImageTag()
	ux.Debugf("FROM image: %s (default core)", tag)
	return tag
}

// statErr returns the error from os.Stat (nil if file exists).
func statErr(path string) error {
	_, err := os.Stat(path)
	return err
}

// IsInitialized returns true when .devcell.toml exists in dir.
func IsInitialized(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".devcell.toml"))
	return err == nil
}

// ScaffoldVagrantfile writes a Vagrantfile to dir substituting:
//   - {{VAGRANT_BOX}}  with vagrantBox  (empty → falls back to MACOS_BOX env var at runtime)
//   - {{NIXHOME_PATH}} with nixhomePath (empty → falls back to NIXHOME_PATH env var at runtime)
//
// Skips writing if a Vagrantfile already exists (idempotent).
func ScaffoldVagrantfile(dir, vagrantBox, nixhomePath string) error {
	dest := filepath.Join(dir, "Vagrantfile")
	if _, err := os.Stat(dest); err == nil {
		return nil // already exists
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	content := bytes.ReplaceAll(vagrantfileContent, []byte("{{VAGRANT_BOX}}"), []byte(vagrantBox))
	content = bytes.ReplaceAll(content, []byte("{{NIXHOME_PATH}}"), []byte(nixhomePath))
	if err := os.WriteFile(dest, content, 0644); err != nil {
		return fmt.Errorf("write Vagrantfile: %w", err)
	}
	return nil
}
