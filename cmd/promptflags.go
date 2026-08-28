package main

import (
	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

// claudePromptFlags materializes both prompt layers and returns the argv that
// points claude at them.
//
// The base flag is emitted only when a base prompt is configured. Claude
// Code's stock prompt is ~10.6 KB of tool guidance and safety instructions,
// and --system-prompt-file discards all of it — so an unconfigured cell must
// keep it.
//
// Only the file forms are emitted: claude rejects the inline and file forms
// of the same layer together, and inline capped the prompt at MAX_ARG_STRLEN
// while exposing its text to `ps aux` and `docker inspect`.
func claudePromptFlags(c config.Config, cellCfg cfg.CellConfig, opts runner.ResolveOpts) ([]string, error) {
	basePath, err := runner.WriteBasePrompt(c, opts)
	if err != nil {
		return nil, err
	}
	overlayPath, err := runner.WriteOverlayPrompt(c, cellCfg, opts)
	if err != nil {
		return nil, err
	}

	var flags []string
	if basePath != "" {
		flags = append(flags, "--system-prompt-file", basePath)
	}
	// The overlay always exists: container context is never empty.
	return append(flags, "--append-system-prompt-file", overlayPath), nil
}
