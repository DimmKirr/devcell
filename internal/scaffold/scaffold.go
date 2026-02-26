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
	"github.com/DimmKirr/devcell/internal/version"
)

//go:embed templates/Dockerfile.tmpl
var dockerfileContent []byte

//go:embed templates/flake.nix.tmpl
var flakeNixContent []byte

//go:embed templates/devcell.toml.tmpl
var devcellTomlContent []byte

type scaffoldFile struct {
	name    string
	content []byte
}

func scaffoldFiles() []scaffoldFile {
	ver := []byte(version.Version)
	df := bytes.ReplaceAll(dockerfileContent, []byte("{{VERSION}}"), ver)
	flake := bytes.ReplaceAll(flakeNixContent, []byte("{{VERSION}}"), ver)
	return []scaffoldFile{
		{"Dockerfile", df},
		{"flake.nix", flake},
		{"devcell.toml", devcellTomlContent},
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
func Scaffold(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	for _, f := range scaffoldFiles() {
		dest := filepath.Join(dir, f.name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.WriteFile(dest, f.content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
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
