// Package runner builds the system prompt that devcell injects into agent
// CLIs (claude, opencode, codex) and the cell serve HTTP server.
//
// The prompt has two distinct conceptual layers, always concatenated in
// order — see ContainerContext and ResolveSystemPrompt — and a third
// per-request layer that lives outside this package (cell serve merges
// per-request `instructions` / `system` role from the API body into the
// user prompt directly).
package runner

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

//go:embed container_context.tmpl.md
var containerContextRaw string

var containerContextTmpl = template.Must(template.New("context").Parse(containerContextRaw))

type volumeMount struct {
	Container string
	Host      string
	Mode      string
}

type contextData struct {
	AppName   string
	AppDir    string
	HostDir   string
	HomeDir   string
	HostHome  string
	ConfigDir string
	Volumes   []volumeMount
}

// ContainerContext returns the auto-generated filesystem/runtime preamble
// — bind mounts, host path mappings, hard constraints — describing the
// devcell container the agent is running inside. Pure container facts;
// no user-controllable content.
//
// This is what makes the agent file-aware: when the user mentions a host
// path, the agent can translate it to the matching container path. Every
// surface that ships a system prompt (cell claude, cell serve) prepends
// this so the agent reasons correctly about its filesystem.
func ContainerContext(c config.Config, cellCfg cfg.CellConfig) string {
	var vols []volumeMount
	for _, vol := range cellCfg.Volumes {
		parts := strings.SplitN(vol.Resolved(), ":", 3)
		if len(parts) >= 2 {
			mode := "read-write"
			if len(parts) == 3 && parts[2] == "ro" {
				mode = "read-only"
			}
			vols = append(vols, volumeMount{
				Container: parts[1],
				Host:      parts[0],
				Mode:      mode,
			})
		}
	}

	data := contextData{
		AppName:   c.AppName,
		AppDir:    "/" + c.AppName,
		HostDir:   c.BaseDir,
		HomeDir:   "/home/" + c.HostUser,
		HostHome:  c.HostHome,
		ConfigDir: c.ConfigDir,
		Volumes:   vols,
	}

	var buf bytes.Buffer
	if err := containerContextTmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("(error rendering container context: %v)\n", err)
	}
	return buf.String()
}

// ResolveOpts bundles every input source the system-prompt resolver looks
// at. Surfaces wire only the inputs they have — `cell claude` leaves the
// flag fields empty; `cell serve` populates everything.
type ResolveOpts struct {
	// FlagFile / FlagInline are the --system-prompt-file / --system-prompt
	// CLI flags. Currently exposed only on `cell serve`.
	FlagFile, FlagInline string
	// EnvFile / EnvInline are the DEVCELL_SYSTEM_PROMPT_FILE /
	// DEVCELL_SYSTEM_PROMPT env vars. Read by every surface.
	EnvFile, EnvInline string
	// AppendFlagFile / AppendFlagInline are the --append-system-prompt-file /
	// --append-system-prompt CLI flags. Currently exposed only on `cell serve`.
	AppendFlagFile, AppendFlagInline string
	// AppendEnvFile / AppendEnvInline are DEVCELL_APPEND_SYSTEM_PROMPT_FILE /
	// DEVCELL_APPEND_SYSTEM_PROMPT. Read by every surface.
	AppendEnvFile, AppendEnvInline string
	// CellCfg supplies [llm].system_prompt / system_prompt_file (base) and
	// [llm].append_system_prompt / append_system_prompt_file (overlay) from
	// the merged devcell.toml.
	CellCfg cfg.CellConfig
	// CfgBaseDir is the project base dir, used to resolve a relative
	// `[llm].system_prompt_file` path. Empty disables relative resolution
	// (absolute paths still work).
	CfgBaseDir string
}

// ResolveSystemPrompt walks the seven-tier source chain in order — flags,
// env, TOML — returning the first match. Within a tier, setting both the
// file and inline form is rejected as ambiguous so the caller never has
// to guess which one won. Across tiers, higher silently shadows lower:
// the layering is the whole point of having multiple sources.
//
// Returns ("", nil) when no source is set, which is the signal that no base
// is configured and Claude Code's built-in prompt must stay in effect.
//
// Resolution order (first match wins):
//
//  1. opts.FlagFile          (--system-prompt-file)
//  2. opts.FlagInline        (--system-prompt)
//  3. opts.EnvFile           (DEVCELL_SYSTEM_PROMPT_FILE)
//  4. opts.EnvInline         (DEVCELL_SYSTEM_PROMPT)
//  5. CellCfg.LLM.SystemPromptFile  ([llm].system_prompt_file)
//  6. CellCfg.LLM.SystemPrompt      ([llm].system_prompt)
//  7. ""
func ResolveSystemPrompt(opts ResolveOpts) (string, error) {
	return resolveTiers(promptSources{
		flagFile:   opts.FlagFile,
		flagInline: opts.FlagInline,
		flagLabels: "--system-prompt and --system-prompt-file",
		envFile:    opts.EnvFile,
		envInline:  opts.EnvInline,
		envLabels:  "DEVCELL_SYSTEM_PROMPT and DEVCELL_SYSTEM_PROMPT_FILE",
		tomlFile:   opts.CellCfg.LLM.SystemPromptFile,
		tomlInline: opts.CellCfg.LLM.SystemPrompt,
		tomlLabels: "[llm].system_prompt and [llm].system_prompt_file",
		fileSource: map[string]string{
			"flag": "--system-prompt-file",
			"env":  "DEVCELL_SYSTEM_PROMPT_FILE",
			"toml": "[llm].system_prompt_file",
		},
	}, opts.CfgBaseDir)
}

// ResolveAppendPrompt walks the same tier chain over the *append* sources.
// It never reads the base sources: after the split, [llm].system_prompt
// replaces Claude Code's built-in prompt while the append layer stacks on
// top of whichever base ends up in effect.
//
// Resolution order (first match wins):
//
//  1. opts.AppendFlagFile   (--append-system-prompt-file)
//  2. opts.AppendFlagInline (--append-system-prompt)
//  3. opts.AppendEnvFile    (DEVCELL_APPEND_SYSTEM_PROMPT_FILE)
//  4. opts.AppendEnvInline  (DEVCELL_APPEND_SYSTEM_PROMPT)
//  5. CellCfg.LLM.AppendSystemPromptFile ([llm].append_system_prompt_file)
//  6. CellCfg.LLM.AppendSystemPrompt     ([llm].append_system_prompt)
//  7. ""
func ResolveAppendPrompt(opts ResolveOpts) (string, error) {
	return resolveTiers(promptSources{
		flagFile:   opts.AppendFlagFile,
		flagInline: opts.AppendFlagInline,
		flagLabels: "--append-system-prompt and --append-system-prompt-file",
		envFile:    opts.AppendEnvFile,
		envInline:  opts.AppendEnvInline,
		envLabels:  "DEVCELL_APPEND_SYSTEM_PROMPT and DEVCELL_APPEND_SYSTEM_PROMPT_FILE",
		tomlFile:   opts.CellCfg.LLM.AppendSystemPromptFile,
		tomlInline: opts.CellCfg.LLM.AppendSystemPrompt,
		tomlLabels: "[llm].append_system_prompt and [llm].append_system_prompt_file",
		fileSource: map[string]string{
			"flag": "--append-system-prompt-file",
			"env":  "DEVCELL_APPEND_SYSTEM_PROMPT_FILE",
			"toml": "[llm].append_system_prompt_file",
		},
	}, opts.CfgBaseDir)
}

// promptSources is one layer's worth of inputs for the tier walk. Both the
// base and the append layer have identical precedence rules, so the walk is
// written once and parameterised by source names for error messages.
type promptSources struct {
	flagFile, flagInline, flagLabels string
	envFile, envInline, envLabels    string
	tomlFile, tomlInline, tomlLabels string
	fileSource                       map[string]string
}

func resolveTiers(src promptSources, cfgBaseDir string) (string, error) {
	if src.flagFile != "" && src.flagInline != "" {
		return "", fmt.Errorf("%s are mutually exclusive", src.flagLabels)
	}
	if src.flagFile != "" {
		return readPromptFile(src.flagFile, src.fileSource["flag"])
	}
	if src.flagInline != "" {
		return src.flagInline, nil
	}

	if src.envFile != "" && src.envInline != "" {
		return "", fmt.Errorf("%s are mutually exclusive", src.envLabels)
	}
	if src.envFile != "" {
		return readPromptFile(src.envFile, src.fileSource["env"])
	}
	if src.envInline != "" {
		return src.envInline, nil
	}

	if src.tomlFile != "" && src.tomlInline != "" {
		return "", fmt.Errorf("%s are mutually exclusive", src.tomlLabels)
	}
	if src.tomlFile != "" {
		path := src.tomlFile
		if !filepath.IsAbs(path) && cfgBaseDir != "" {
			path = filepath.Join(cfgBaseDir, path)
		}
		return readPromptFile(path, src.fileSource["toml"])
	}
	return src.tomlInline, nil
}

// AssembleOverlayPrompt builds the overlay: the auto-generated container
// context followed by the resolved append prompt. This is what reaches claude
// via --append-system-prompt-file.
//
// Container context lives on the overlay rather than the base deliberately —
// it is regenerated per run from live container facts, so a user-supplied
// base prompt must not be able to displace it.
//
// When the append layer resolves empty, the container context alone is
// returned; it is never empty, so the overlay file is always written.
func AssembleOverlayPrompt(c config.Config, cellCfg cfg.CellConfig, opts ResolveOpts) (string, error) {
	resolved, err := ResolveAppendPrompt(opts)
	if err != nil {
		return "", err
	}
	ctx := ContainerContext(c, cellCfg)
	if resolved == "" {
		return ctx, nil
	}
	if !strings.HasSuffix(resolved, "\n") {
		resolved += "\n"
	}
	return ctx + "\n" + resolved, nil
}

func readPromptFile(path, source string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", source, err)
	}
	return string(b), nil
}
