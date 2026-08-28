package runner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
)

// promptDirName is the project-relative directory holding generated prompt
// files. It sits under .devcell/ because that tree is gitignored build
// output — these files are transport, regenerated on every launch, never
// hand-edited.
const promptDirName = "prompts"

// OverlayPromptFile is the generated file carrying container context plus the
// resolved operator prompt — everything that layers *on top of* whichever base
// prompt is in effect.
const OverlayPromptFile = "additional-systemprompt.md"

// BasePromptFile is the generated file carrying the base prompt — the one
// that REPLACES Claude Code's built-in prompt.
const BasePromptFile = "system-prompt.md"

// WriteOverlayPrompt assembles the overlay and materializes it, returning the
// container path for --append-system-prompt-file.
//
// Container context is always present, so this file is always written even
// when no append prompt is configured.
func WriteOverlayPrompt(c config.Config, cellCfg cfg.CellConfig, opts ResolveOpts) (string, error) {
	content, err := AssembleOverlayPrompt(c, cellCfg, opts)
	if err != nil {
		return "", err
	}
	return WritePromptFile(c, OverlayPromptFile, content)
}

// WriteBasePrompt materializes the base prompt, returning the container path
// for --system-prompt-file — or "" when no base is configured.
//
// The empty return is the switch that keeps this opt-in: with no base set,
// the caller emits no flag and Claude Code's stock prompt stays in effect.
// The content is written verbatim; container context belongs on the overlay.
func WriteBasePrompt(c config.Config, opts ResolveOpts) (string, error) {
	content, err := ResolveSystemPrompt(opts)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", nil
	}
	return WritePromptFile(c, BasePromptFile, content)
}

// WritePromptFile materializes prompt content to disk and returns the path
// the *container* will read it at.
//
// Prompts used to travel as a single argv element. That capped them at
// MAX_ARG_STRLEN (128 KiB on Linux) and published their full text to
// `ps aux` and `docker inspect`. Writing a file and passing its path
// removes both limits — claude reads it via --system-prompt-file /
// --append-system-prompt-file.
//
// Files are namespaced per cell. .devcell/ is per project, but a container is
// per (cell, project) pair and ContainerContext embeds the cell name, so a
// shared path would let one cell boot with another cell's container context.
//
// The returned path is the container-side path: the project is bind-mounted
// at /<AppName>, so translating is a prefix swap of BaseDir for that root.
func WritePromptFile(c config.Config, name, content string) (string, error) {
	cell := c.CellName
	if cell == "" {
		cell = "main"
	}

	hostDir := filepath.Join(c.BaseDir, ".devcell", promptDirName, cell)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return "", fmt.Errorf("create prompt dir %s: %w", hostDir, err)
	}

	hostPath := filepath.Join(hostDir, name)
	if err := os.WriteFile(hostPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write prompt file %s: %w", hostPath, err)
	}

	return path.Join("/"+c.AppName, ".devcell", promptDirName, cell, name), nil
}
