package cfg

import (
	"os"

	"github.com/BurntSushi/toml"
)

// CellSection holds [cell] config.
type CellSection struct {
	ImageTag string `toml:"image_tag"`
	GUI      bool   `toml:"gui"`
	Timezone string `toml:"timezone"` // IANA tz (e.g. "Europe/Prague"); default: host $TZ
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

// LLMProvider holds a single provider entry under [llm.models.providers.<name>].
type LLMProvider struct {
	BaseURL string   `toml:"base_url"`
	Models  []string `toml:"models"`
}

// LLMModelsSection holds [llm.models] config — provider/model declarations.
type LLMModelsSection struct {
	Default   string                 `toml:"default"`
	Providers map[string]LLMProvider `toml:"providers"`
}

// LLMSection holds [llm] config — all AI agent settings in one place.
type LLMSection struct {
	SystemPrompt string           `toml:"system_prompt"`
	UseOllama    bool             `toml:"use_ollama"`
	Models       LLMModelsSection `toml:"models"`
}

// GitSection holds [git] config for git identity inside the container.
type GitSection struct {
	AuthorName     string `toml:"author_name"`
	AuthorEmail    string `toml:"author_email"`
	CommitterName  string `toml:"committer_name"`
	CommitterEmail string `toml:"committer_email"`
}

// HasIdentity reports whether any git identity field is set.
func (g GitSection) HasIdentity() bool {
	return g.AuthorName != "" || g.AuthorEmail != "" ||
		g.CommitterName != "" || g.CommitterEmail != ""
}

// ResolvedCommitterName returns CommitterName if set, else falls back to AuthorName.
func (g GitSection) ResolvedCommitterName() string {
	if g.CommitterName != "" {
		return g.CommitterName
	}
	return g.AuthorName
}

// ResolvedCommitterEmail returns CommitterEmail if set, else falls back to AuthorEmail.
func (g GitSection) ResolvedCommitterEmail() string {
	if g.CommitterEmail != "" {
		return g.CommitterEmail
	}
	return g.AuthorEmail
}

// OpSection holds [op] config for 1Password secret injection.
type OpSection struct {
	Items []string `toml:"items"` // 1Password item names to resolve via `op item get`
}

// CellConfig is the merged configuration from all TOML layers.
type CellConfig struct {
	Cell     CellSection
	LLM      LLMSection `toml:"llm"`
	Git      GitSection `toml:"git"`
	Op       OpSection  `toml:"op"`
	Env      map[string]string
	Mise     map[string]string `toml:"mise"` // [mise] — keys map to MISE_<UPPER_KEY> env vars
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
		Mise: make(map[string]string),
	}

	// Copy global env
	for k, v := range global.Env {
		out.Env[k] = v
	}
	// Project overrides / extends
	for k, v := range project.Env {
		out.Env[k] = v
	}

	// Mise: same accumulate semantics as Env
	for k, v := range global.Mise {
		out.Mise[k] = v
	}
	for k, v := range project.Mise {
		out.Mise[k] = v
	}

	// Scalars: project wins when non-zero
	if project.Cell.ImageTag != "" {
		out.Cell.ImageTag = project.Cell.ImageTag
	}
	if project.Cell.GUI {
		out.Cell.GUI = true
	}
	if project.Cell.Timezone != "" {
		out.Cell.Timezone = project.Cell.Timezone
	}

	// LLM: project wins for scalars, providers accumulate
	out.LLM = global.LLM
	if project.LLM.SystemPrompt != "" {
		out.LLM.SystemPrompt = project.LLM.SystemPrompt
	}
	if project.LLM.UseOllama {
		out.LLM.UseOllama = true
	}

	// Git: project wins when non-zero
	out.Git = global.Git
	if project.Git.AuthorName != "" {
		out.Git.AuthorName = project.Git.AuthorName
	}
	if project.Git.AuthorEmail != "" {
		out.Git.AuthorEmail = project.Git.AuthorEmail
	}
	if project.Git.CommitterName != "" {
		out.Git.CommitterName = project.Git.CommitterName
	}
	if project.Git.CommitterEmail != "" {
		out.Git.CommitterEmail = project.Git.CommitterEmail
	}

	// Op items: accumulate, project appended after global (deduped)
	seen := make(map[string]bool, len(global.Op.Items))
	for _, item := range global.Op.Items {
		out.Op.Items = append(out.Op.Items, item)
		seen[item] = true
	}
	for _, item := range project.Op.Items {
		if !seen[item] {
			out.Op.Items = append(out.Op.Items, item)
		}
	}

	// Slices accumulate: global first, then project
	out.Volumes = append(global.Volumes, project.Volumes...)

	// LLM models: project default wins, providers accumulate (project wins on key conflict)
	if project.LLM.Models.Default != "" {
		out.LLM.Models.Default = project.LLM.Models.Default
	}
	if len(global.LLM.Models.Providers) > 0 || len(project.LLM.Models.Providers) > 0 {
		out.LLM.Models.Providers = make(map[string]LLMProvider)
		for k, v := range global.LLM.Models.Providers {
			out.LLM.Models.Providers[k] = v
		}
		for k, v := range project.LLM.Models.Providers {
			out.LLM.Models.Providers[k] = v
		}
	}

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
