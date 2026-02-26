package cfg

import (
	"os"

	"github.com/BurntSushi/toml"
)

// CellSection holds [cell] config.
type CellSection struct {
	ImageTag string `toml:"image_tag"`
	GUI      bool   `toml:"gui"`
}

// VolumeMount holds a single [[volumes]] entry.
type VolumeMount struct {
	Mount string `toml:"mount"`
}

// PackagesSection holds [packages] config for npm and python tools.
type PackagesSection struct {
	Npm    map[string]string `toml:"npm"`
	Python map[string]string `toml:"python"`
}

// CellConfig is the merged configuration from all TOML layers.
type CellConfig struct {
	Cell     CellSection
	Env      map[string]string
	Volumes  []VolumeMount
	Packages PackagesSection
}

// LoadFile parses a TOML file into CellConfig.
// Returns zero value + nil error if the file does not exist.
func LoadFile(path string) (CellConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CellConfig{}, nil
		}
		return CellConfig{}, err
	}
	var c CellConfig
	if _, err := toml.Decode(string(data), &c); err != nil {
		return CellConfig{}, err
	}
	return c, nil
}

// Merge returns a new CellConfig with project overriding global for scalars;
// Env maps and Volumes slices are accumulated (project wins on key conflict).
func Merge(global, project CellConfig) CellConfig {
	out := CellConfig{
		Cell: global.Cell,
		Env:  make(map[string]string),
	}

	// Copy global env
	for k, v := range global.Env {
		out.Env[k] = v
	}
	// Project overrides / extends
	for k, v := range project.Env {
		out.Env[k] = v
	}

	// Scalars: project wins when non-zero
	if project.Cell.ImageTag != "" {
		out.Cell.ImageTag = project.Cell.ImageTag
	}
	if project.Cell.GUI {
		out.Cell.GUI = true
	}

	// Slices accumulate: global first, then project
	out.Volumes = append(global.Volumes, project.Volumes...)

	return out
}

// ApplyEnv overrides scalar fields from environment variables.
func ApplyEnv(c *CellConfig, getenv func(string) string) {
	if tag := getenv("IMAGE_TAG"); tag != "" {
		c.Cell.ImageTag = tag
	}
}

// LoadLayered loads global + project files, merges them, then applies env overrides.
func LoadLayered(globalPath, projectPath string, getenv func(string) string) CellConfig {
	global, _ := LoadFile(globalPath)
	project, _ := LoadFile(projectPath)
	merged := Merge(global, project)
	ApplyEnv(&merged, getenv)
	return merged
}

// LoadFromOS loads the layered config using real XDG paths and os.Getenv.
func LoadFromOS(configDir, cwd string) CellConfig {
	globalPath := configDir + "/devcell.toml"
	projectPath := cwd + "/.devcell.toml"
	return LoadLayered(globalPath, projectPath, os.Getenv)
}
