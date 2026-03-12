package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ollama"
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

On first run, if no opencode config exists at $CELL_HOME/.config/opencode/,
locally available ollama models are auto-detected and written there.
When [models] is configured in devcell.toml, those models are used instead.

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

// opencodeConfigPath returns the path to opencode's config file in the cell
// home directory. This is the same .opencode.json that the entrypoint merges
// MCP servers into, so models and MCP coexist in one file.
func opencodeConfigPath(cellHome string) string {
	return filepath.Join(cellHome, ".opencode.json")
}

// opencodeEnv returns extra env vars for the opencode container.
//
// On every run it detects available models (from devcell.toml or ollama) and
// merges them into $CELL_HOME/.opencode.json, preserving any existing keys
// (MCP servers, user customizations). The merged result is also injected
// via OPENCODE_CONFIG_CONTENT.
func opencodeEnv() map[string]string {
	dbg := scanFlag("--debug")
	c, err := config.LoadFromOS()
	if err != nil {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: config load failed, using minimal config\n")
		}
		return map[string]string{
			"OPENCODE_CONFIG_CONTENT": string(buildOpencodeJSON(cfg.ModelsSection{})),
		}
	}

	// Resolve models: devcell.toml [models] > auto-detect ollama > empty.
	cellCfg := cfg.LoadFromOS(c.ConfigDir, c.BaseDir)
	models := cellCfg.Models
	if len(models.Providers) > 0 {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: using models from devcell.toml [models]\n")
		}
	} else {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: no [models] in devcell.toml, probing ollama...\n")
		}
		models = autoDetectOllamaModels()
	}

	if dbg {
		if models.Default != "" {
			fmt.Fprintf(os.Stderr, " opencode: default model: %s\n", models.Default)
		} else {
			fmt.Fprintf(os.Stderr, " opencode: no models detected\n")
		}
	}

	configPath := opencodeConfigPath(c.CellHome)

	// Build the model/provider portion of the config.
	modelData := buildOpencodeJSON(models)

	// Merge into existing .opencode.json (preserves MCP servers, user keys).
	merged, err := mergeOpencodeConfig(configPath, modelData, dbg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not merge opencode config: %v\n", err)
		merged = modelData
	}

	// Write merged result back to disk.
	if err := writeOpencodeConfig(configPath, merged); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write opencode config: %v\n", err)
	} else if dbg {
		fmt.Fprintf(os.Stderr, " opencode: config written to %s\n", configPath)
	}

	return map[string]string{
		"OPENCODE_CONFIG_CONTENT": string(merged),
	}
}

// mergeOpencodeConfig reads an existing .opencode.json and merges model/provider
// fields from newData into it. Existing keys (mcp, user customizations) are
// preserved. Model fields ($schema, permission, model, provider) are overwritten.
func mergeOpencodeConfig(path string, newData []byte, dbg bool) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: no existing config, creating new\n")
		}
		return newData, nil
	}

	// Parse existing config as generic map to preserve unknown keys.
	var base map[string]json.RawMessage
	if err := json.Unmarshal(existing, &base); err != nil {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: existing config invalid JSON, replacing\n")
		}
		return newData, nil
	}

	// Parse new model data.
	var overlay map[string]json.RawMessage
	if err := json.Unmarshal(newData, &overlay); err != nil {
		return nil, fmt.Errorf("parse model data: %w", err)
	}

	// Merge: overlay keys overwrite base keys.
	for k, v := range overlay {
		base[k] = v
	}

	if dbg {
		fmt.Fprintf(os.Stderr, " opencode: merged models into existing config (%d keys)\n", len(base))
	}

	return json.Marshal(base)
}

// autoDetectOllamaModels probes the local ollama instance for available models,
// ranks them by SWE-bench fallback ratings, and returns a ModelsSection with
// all detected models and the #1 ranked model as default.
func autoDetectOllamaModels() cfg.ModelsSection {
	dbg := scanFlag("--debug")
	ctx := context.Background()
	if !ollama.Detect(ctx, ollama.DefaultBaseURL) {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: ollama not detected at %s\n", ollama.DefaultBaseURL)
		}
		return cfg.ModelsSection{}
	}
	models, err := ollama.FetchModels(ctx, ollama.DefaultBaseURL)
	if err != nil || len(models) == 0 {
		if dbg {
			fmt.Fprintf(os.Stderr, " opencode: no models found from ollama\n")
		}
		return cfg.ModelsSection{}
	}
	if dbg {
		fmt.Fprintf(os.Stderr, " opencode: found %d models from ollama\n", len(models))
	}
	ranked := ollama.RankModels(models, 0, nil, nil)
	if len(ranked) == 0 {
		return cfg.ModelsSection{}
	}

	if dbg {
		for _, r := range ranked {
			fmt.Fprintf(os.Stderr, " opencode:   #%d %s (score: %.1f, source: %s)\n", r.Rank, r.Name, r.SWEScore, r.ScoreSource)
		}
		fmt.Fprintf(os.Stderr, " opencode: best → %s\n", ranked[0].Name)
	}

	var names []string
	for _, r := range ranked {
		names = append(names, r.Name)
	}

	// Only provide the model list — don't auto-select a default.
	// The user picks in opencode's UI, or sets [models] default in devcell.toml.
	return cfg.ModelsSection{
		Providers: map[string]cfg.LLMProvider{
			"ollama": {Models: names},
		},
	}
}

// writeOpencodeConfig writes JSON data to the opencode config path,
// creating parent directories as needed.
func writeOpencodeConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Pretty-print for user readability.
	var pretty json.RawMessage = data
	formatted, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		formatted = data
	}
	return os.WriteFile(path, append(formatted, '\n'), 0644)
}

// fileExists returns true if path exists and is a regular file.
var fileExists = defaultFileExists

func defaultFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
