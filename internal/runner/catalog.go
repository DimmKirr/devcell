package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
)

// ModuleMeta is one entry from the `devcellModules` flake catalog.
// Mirrors the shape of `nix eval .#devcellModules --json`.
type ModuleMeta struct {
	Description string   `json:"description"`
	MCPServers  []string `json:"mcpServers"`
	SizeMB      int      `json:"sizeMb"`
}

// Catalog is the full catalog keyed by module name (e.g. "electronics", "yahoo-finance").
type Catalog map[string]ModuleMeta

// Names returns the catalog keys sorted alphabetically. Used by
// `devcell modules list` output and validation error messages.
func (c Catalog) Names() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ParseCatalogJSON decodes raw JSON output from `nix eval .#devcellModules --json`
// into a typed Catalog. Returns an error if the JSON is malformed.
func ParseCatalogJSON(raw []byte) (Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return c, nil
}

// ReadCatalogFromFlake invokes `nix eval --json <flakeRef>#devcellModules`
// and parses the result. `flakeRef` is something like
// `path:./nixhome` or `github:devcell/modules?ref=v1.0`.
//
// Returns an error if the nix subprocess fails or the JSON is malformed.
// The CLI wraps this error with user-language guidance in cmd/.
func ReadCatalogFromFlake(ctx context.Context, flakeRef string) (Catalog, error) {
	cmd := exec.CommandContext(ctx, "nix",
		"eval", "--json",
		"--extra-experimental-features", "nix-command flakes",
		flakeRef+"#devcellModules",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nix eval %s#devcellModules: %w", flakeRef, err)
	}
	return ParseCatalogJSON(out)
}
