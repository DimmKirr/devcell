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
const defaultModelsSection = `# [models]
# Default LLM model (format: provider/model). Used by opencode and other agents.
# default = "ollama/deepseek-r1:32b"

# [models.providers.ollama]
# models = ["deepseek-r1:32b", "qwen3:8b"]

# [models.providers.lmstudio]
# base_url = "http://host.docker.internal:1234/v1"
# models = ["deepseek-r1:32b"]`

func scaffoldFiles(modelsSnippet string) []scaffoldFile {
	dockerfile := bytes.ReplaceAll(dockerfileContent, []byte("{{BASE_IMAGE}}"), []byte(runner.BaseImageTag()))
	flake := bytes.ReplaceAll(flakeNixContent, []byte("{{VERSION}}"), []byte(version.Version))
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
// Files that already exist are skipped (idempotent).
// modelsSnippet is an optional commented-out [models] section for devcell.toml;
// pass "" to use the default generic example.
func Scaffold(dir string, modelsSnippet string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	for _, f := range scaffoldFiles(modelsSnippet) {
		dest := filepath.Join(dir, f.name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	// Scaffold homedir/.config/starship.toml for per-project prompt customization.
	starshipDir := filepath.Join(dir, "homedir", ".config")
	starshipDest := filepath.Join(starshipDir, "starship.toml")
	if _, err := os.Stat(starshipDest); err != nil {
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
