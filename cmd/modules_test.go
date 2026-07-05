package main

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestFormatCatalogList: given a Catalog, the human-facing output should
// list each module on its own line with name + size + description.
// Tests the formatting layer in isolation (no nix invocation).
func TestFormatCatalogList_NameAndDescriptionVisible(t *testing.T) {
	cat := runner.Catalog{
		"electronics": {
			Description: "KiCad EDA, SPICE",
			MCPServers:  []string{"kicad-mcp"},
			SizeMB:      800,
		},
		"plex": {
			Description: "Plex Media Server MCP",
			MCPServers:  []string{"plex"},
			SizeMB:      80,
		},
	}
	out := formatCatalogList(cat)
	for _, want := range []string{
		"electronics",
		"plex",
		"KiCad EDA",
		"Plex Media Server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// Names must be sorted alphabetically for reproducible output.
func TestFormatCatalogList_NamesSorted(t *testing.T) {
	cat := runner.Catalog{
		"zebra":       {Description: "z"},
		"apple":       {Description: "a"},
		"electronics": {Description: "e"},
	}
	out := formatCatalogList(cat)
	idxApple := strings.Index(out, "apple")
	idxElec := strings.Index(out, "electronics")
	idxZebra := strings.Index(out, "zebra")
	if !(idxApple < idxElec && idxElec < idxZebra) {
		t.Errorf("rows not sorted alphabetically (apple=%d electronics=%d zebra=%d):\n%s",
			idxApple, idxElec, idxZebra, out)
	}
}

// Modules with MCP servers should surface the server names so users know
// which MCP gets activated when they add the module.
func TestFormatCatalogList_ShowsMCPServerNames(t *testing.T) {
	cat := runner.Catalog{
		"travel": {
			Description: "Maps + TripIt",
			MCPServers:  []string{"google-maps", "tripit"},
			SizeMB:      100,
		},
	}
	out := formatCatalogList(cat)
	for _, want := range []string{"google-maps", "tripit"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing MCP name %q in output:\n%s", want, out)
		}
	}
}
