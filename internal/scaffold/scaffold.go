package scaffold

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/DimmKirr/devcell/internal/version"
)

//go:embed templates/Dockerfile.tmpl
var dockerfileContent []byte

//go:embed templates/flake.nix.tmpl
var flakeNixContent []byte

//go:embed templates/devcell.toml.tmpl
var devcellTomlContent []byte

//go:embed templates/devcell.project.toml.tmpl
var devcellProjectTomlContent []byte

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

func scaffoldFiles(modelsSnippet, nixhomePath string) []scaffoldFile {
	dockerfile := bytes.ReplaceAll(dockerfileContent, []byte("{{BASE_IMAGE}}"), []byte(runner.BaseImageTag()))
	flake := bytes.ReplaceAll(flakeNixContent, []byte("{{VERSION}}"), []byte(version.Version))

	// When nixhomePath is set, use local path input and add COPY nixhome/ to Dockerfile.
	if nixhomePath != "" {
		// Replace the github: URL line with path:./nixhome
		flake = bytes.ReplaceAll(flake,
			[]byte(`inputs.devcell.url = "github:DimmKirr/devcell/`+version.Version+`?dir=nixhome";`),
			[]byte(`inputs.devcell.url = "path:./nixhome";`))

		// Insert COPY nixhome/ before the existing COPY flake.* line
		nixhomeCopy := []byte("COPY --chown=devcell:usergroup nixhome/ /opt/devcell/.config/devcell/nixhome/\n")
		flakeCopyLine := []byte("COPY --chown=devcell:usergroup flake.*")
		dockerfile = bytes.Replace(dockerfile, flakeCopyLine, append(nixhomeCopy, flakeCopyLine...), 1)
	}

	models := modelsSnippet
	if models == "" {
		models = defaultModelsSection
	}
	tomlContent := bytes.ReplaceAll(devcellTomlContent, []byte("{{MODELS_SECTION}}"), []byte(models))
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

// Scaffold writes scaffold files to dir, then generates package.json and
// pyproject.toml from the [packages] section in devcell.toml.
// Files that already exist are skipped (idempotent) unless force is true.
// modelsSnippet is an optional commented-out [models] section for devcell.toml;
// pass "" to use the default generic example.

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
	// Remove stale copy so we get a clean sync every build.
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove old nixhome: %w", err)
	}
	// Remove stale outer flake.lock — inner nixhome has its own lock
	// that matches the base image's nix store.
	os.Remove(filepath.Join(configDir, "flake.lock"))
	return copyDir(srcPath, dest)
}

// copyDir recursively copies src directory to dst.
func copyDir(src, dst string) error {
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

func Scaffold(dir string, modelsSnippet string, nixhomePath string, force bool) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	for _, f := range scaffoldFiles(modelsSnippet, nixhomePath) {
		dest := filepath.Join(dir, f.name)
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
	starshipDir := filepath.Join(dir, "homedir", ".config")
	starshipDest := filepath.Join(starshipDir, "starship.toml")
	if force || os.IsNotExist(statErr(starshipDest)) {
		if err := os.MkdirAll(starshipDir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", starshipDir, err)
		}
		if err := os.WriteFile(starshipDest, starshipTomlContent, 0644); err != nil {
			return fmt.Errorf("write homedir starship.toml: %w", err)
		}
	}

	// Generate package files from devcell.toml [packages] config.
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	generated := []scaffoldFile{
		{"package.json", generatePackageJSON(c.Packages.Npm)},
		{"pyproject.toml", generatePyprojectTOML(c.Packages.Python)},
	}
	for _, f := range generated {
		dest := filepath.Join(dir, f.name)
		// Always regenerate — these are derived from devcell.toml.
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// RegeneratePackageFiles regenerates package.json and pyproject.toml from devcell.toml.
// Call this before any build to ensure derived files are in sync with config.
func RegeneratePackageFiles(configDir string) error {
	c, err := cfg.LoadFile(filepath.Join(configDir, "devcell.toml"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	generated := []scaffoldFile{
		{"package.json", generatePackageJSON(c.Packages.Npm)},
		{"pyproject.toml", generatePyprojectTOML(c.Packages.Python)},
	}
	for _, f := range generated {
		dest := filepath.Join(configDir, f.name)
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// statErr returns the error from os.Stat (nil if file exists).
func statErr(path string) error {
	_, err := os.Stat(path)
	return err
}

// IsInitialized returns true when devcell.toml exists in dir.
func IsInitialized(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "devcell.toml"))
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

// ScaffoldProject writes a .devcell.toml in the given directory.
// Returns os.ErrExist if the file already exists.
func ScaffoldProject(dir string) error {
	dest := filepath.Join(dir, ".devcell.toml")
	if _, err := os.Stat(dest); err == nil {
		return os.ErrExist
	}
	return os.WriteFile(dest, devcellProjectTomlContent, 0644)
}

// ScaffoldProjectForce writes a .devcell.toml, overwriting if it exists.
func ScaffoldProjectForce(dir string) error {
	return os.WriteFile(filepath.Join(dir, ".devcell.toml"), devcellProjectTomlContent, 0644)
}
