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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

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
	var b strings.Builder

	appDir := "/" + c.AppName // e.g. /devcell-85
	hostDir := c.BaseDir      // e.g. /Users/dmitry/dev/dimmkirr/devcell
	homeDir := "/home/" + c.HostUser

	fmt.Fprintf(&b, "Environment: Docker container (cell-%s)\n", c.AppName)
	fmt.Fprintf(&b, "Project: %s (alias for %s on host)\n", appDir, hostDir)
	fmt.Fprintf(&b, "Both paths are bind-mounted from the same host directory and resolve to the same filesystem.\n")
	fmt.Fprintf(&b, "Working directory is %s. If the user mentions host paths like %s/..., they map to %s/...\n", appDir, hostDir, appDir)
	b.WriteString("\n")

	b.WriteString("Bind mounts:\n")
	fmt.Fprintf(&b, "  %s = %s (project source, read-write)\n", appDir, hostDir)
	fmt.Fprintf(&b, "  %s (persistent home, survives container restarts)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/skills (read-write)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/commands (read-only, from host)\n", homeDir)
	fmt.Fprintf(&b, "  %s/.claude/agents (read-only, from host)\n", homeDir)
	fmt.Fprintf(&b, "  /etc/devcell/config = %s (user build config)\n", c.ConfigDir)

	for _, vol := range cellCfg.Volumes {
		parts := strings.SplitN(vol.Resolved(), ":", 3)
		if len(parts) >= 2 {
			mode := "read-write"
			if len(parts) == 3 && parts[2] == "ro" {
				mode = "read-only"
			}
			fmt.Fprintf(&b, "  %s = %s (%s, from devcell.toml)\n", parts[1], parts[0], mode)
		}
	}
	b.WriteString("\n")

	b.WriteString("Host path mapping (use these to translate paths the user mentions):\n")
	fmt.Fprintf(&b, "  host: %s → container: %s\n", hostDir, hostDir)
	fmt.Fprintf(&b, "  host: %s → container: %s\n", c.HostHome, homeDir)
	for _, vol := range cellCfg.Volumes {
		parts := strings.SplitN(vol.Resolved(), ":", 3)
		if len(parts) >= 2 {
			fmt.Fprintf(&b, "  host: %s → container: %s\n", parts[0], parts[1])
		}
	}
	b.WriteString("\n")

	b.WriteString("Constraints:\n")
	b.WriteString("  - /opt/devcell is the nix environment — do not modify at runtime\n")
	b.WriteString("  - Nix profile: /opt/devcell/.local/state/nix/profiles/profile\n")

	return b.String()
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
	// CellCfg supplies [llm].system_prompt and [llm].system_prompt_file
	// from the merged devcell.toml.
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
// Returns ("", nil) when no source is set — callers concatenate this
// with ContainerContext via AssembleSystemPrompt.
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
	if opts.FlagFile != "" && opts.FlagInline != "" {
		return "", fmt.Errorf("--system-prompt and --system-prompt-file are mutually exclusive")
	}
	if opts.FlagFile != "" {
		return readPromptFile(opts.FlagFile, "--system-prompt-file")
	}
	if opts.FlagInline != "" {
		return opts.FlagInline, nil
	}

	if opts.EnvFile != "" && opts.EnvInline != "" {
		return "", fmt.Errorf("DEVCELL_SYSTEM_PROMPT and DEVCELL_SYSTEM_PROMPT_FILE are mutually exclusive")
	}
	if opts.EnvFile != "" {
		return readPromptFile(opts.EnvFile, "DEVCELL_SYSTEM_PROMPT_FILE")
	}
	if opts.EnvInline != "" {
		return opts.EnvInline, nil
	}

	tomlFile := opts.CellCfg.LLM.SystemPromptFile
	tomlInline := opts.CellCfg.LLM.SystemPrompt
	if tomlFile != "" && tomlInline != "" {
		return "", fmt.Errorf("[llm].system_prompt and [llm].system_prompt_file are mutually exclusive")
	}
	if tomlFile != "" {
		// Resolve relative paths against the project base dir, matching
		// the convention `[[volumes]]` already uses.
		path := tomlFile
		if !filepath.IsAbs(path) && opts.CfgBaseDir != "" {
			path = filepath.Join(opts.CfgBaseDir, path)
		}
		return readPromptFile(path, "[llm].system_prompt_file")
	}
	return tomlInline, nil
}

// AssembleSystemPrompt is the single entry point callers should use to
// build the string passed to claude's --append-system-prompt (or any
// future agent's equivalent). It prepends ContainerContext to the
// resolved prompt with a blank-line separator. When the resolved prompt
// is empty, returns just ContainerContext.
func AssembleSystemPrompt(c config.Config, cellCfg cfg.CellConfig, opts ResolveOpts) (string, error) {
	resolved, err := ResolveSystemPrompt(opts)
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
