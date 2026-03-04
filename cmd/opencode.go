package main

import (
	"encoding/json"
	"sort"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/spf13/cobra"
)

// knownProviderDefaults maps provider names to their default base URLs
// inside docker (host.docker.internal).
var knownProviderDefaults = map[string]string{
	"ollama":   "http://host.docker.internal:11434/v1",
	"lmstudio": "http://host.docker.internal:1234/v1",
}

var opencodeCmd = &cobra.Command{
	Use:   "opencode [args...]",
	Short: "Run OpenCode in a devcell container",
	Long: `Starts an OpenCode session inside an isolated devcell container.

The current working directory is mounted as /workspace.
All additional args are forwarded to the opencode binary unchanged.

When [models] is configured in devcell.toml, provider and permission
settings are injected via OPENCODE_CONFIG_CONTENT (highest precedence).

Examples:

    cell opencode
    cell opencode --model anthropic/claude-sonnet-4-5`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// If the user passed no arguments (after stripping devcell flags),
		// default to "opencode ." so it opens in the current directory.
		cleaned := stripCellFlags(args)
		if len(cleaned) == 0 {
			args = append(args, ".")
		}
		// Translate devcell --debug to opencode --log-level DEBUG
		var defaultFlags []string
		if scanFlag("--debug") {
			defaultFlags = append(defaultFlags, "--log-level", "DEBUG")
		}
		return runAgent("opencode", defaultFlags, args, opencodeEnv())
	},
}

var opencodeResumeCmd = &cobra.Command{
	Use:   "resume [args...]",
	Short: "Resume an OpenCode session",
	Long: `Resumes a previous OpenCode session inside a devcell container.

All additional args are forwarded to 'opencode resume' unchanged.

Examples:

    cell opencode resume`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgent("opencode", nil, append([]string{"resume"}, args...), opencodeEnv())
	},
}

func init() {
	opencodeCmd.AddCommand(opencodeResumeCmd)
}

// opencodeEnv returns extra env vars for the opencode container.
// Always injects OPENCODE_CONFIG_CONTENT with at least permission:"allow".
// When [models] is configured in devcell.toml, includes provider/model config.
func opencodeEnv() map[string]string {
	return map[string]string{
		"OPENCODE_CONFIG_CONTENT": string(buildOpencodeJSON(loadModelsSection())),
	}
}

// loadModelsSection loads the [models] section from devcell.toml.
// Returns zero value if config can't be loaded (non-fatal).
func loadModelsSection() cfg.ModelsSection {
	c, err := config.LoadFromOS()
	if err != nil {
		return cfg.ModelsSection{}
	}
	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	return cellCfg.Models
}

// opencodeJSON is the structure marshalled to opencode.json.
type opencodeJSON struct {
	Schema     string                          `json:"$schema"`
	Permission string                          `json:"permission"`
	Model      string                          `json:"model,omitempty"`
	Provider   map[string]opencodeProviderJSON `json:"provider"`
}

type opencodeProviderJSON struct {
	NPM     string                       `json:"npm"`
	Options map[string]string             `json:"options"`
	Models  map[string]opencodeModelJSON  `json:"models"`
}

type opencodeModelJSON struct {
	Name string `json:"name"`
}

// buildOpencodeJSON generates opencode config JSON from the [models] section.
// Always includes permission:"allow" for sandbox auto-approval.
func buildOpencodeJSON(ms cfg.ModelsSection) []byte {
	doc := opencodeJSON{
		Schema:     "https://opencode.ai/config.json",
		Permission: "allow",
		Model:      ms.Default,
		Provider:   make(map[string]opencodeProviderJSON),
	}

	// Sort provider names for deterministic output
	names := make([]string, 0, len(ms.Providers))
	for name := range ms.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prov := ms.Providers[name]

		baseURL := prov.BaseURL
		if baseURL == "" {
			baseURL = knownProviderDefaults[name]
		}

		models := make(map[string]opencodeModelJSON, len(prov.Models))
		for _, m := range prov.Models {
			models[m] = opencodeModelJSON{Name: m}
		}

		doc.Provider[name] = opencodeProviderJSON{
			NPM:     "@ai-sdk/openai-compatible",
			Options: map[string]string{"baseURL": baseURL},
			Models:  models,
		}
	}

	data, _ := json.Marshal(doc)
	return data
}
