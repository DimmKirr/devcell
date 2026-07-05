package runner_test

import (
	"reflect"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestParseCatalogJSON_HappyPath: feed JSON in the shape that
// `nix eval .#devcellModules --json` returns and verify it round-trips
// to a typed catalog. The reader is split from the subprocess call so
// it can be tested without invoking nix.
func TestParseCatalogJSON_HappyPath(t *testing.T) {
	raw := []byte(`{
		"electronics": {
			"description": "KiCad EDA, SPICE simulation",
			"mcpServers": ["kicad-mcp"],
			"sizeMb": 800
		},
		"yahoo-finance": {
			"description": "Yahoo Finance market data",
			"mcpServers": ["yahoo-finance"],
			"sizeMb": 50
		}
	}`)

	cat, err := runner.ParseCatalogJSON(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(cat) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(cat), cat)
	}

	wantElectronics := runner.ModuleMeta{
		Description: "KiCad EDA, SPICE simulation",
		MCPServers:  []string{"kicad-mcp"},
		SizeMB:      800,
	}
	if !reflect.DeepEqual(cat["electronics"], wantElectronics) {
		t.Errorf("electronics: got %+v, want %+v", cat["electronics"], wantElectronics)
	}

	wantYahoo := runner.ModuleMeta{
		Description: "Yahoo Finance market data",
		MCPServers:  []string{"yahoo-finance"},
		SizeMB:      50,
	}
	if !reflect.DeepEqual(cat["yahoo-finance"], wantYahoo) {
		t.Errorf("yahoo-finance: got %+v, want %+v", cat["yahoo-finance"], wantYahoo)
	}
}

func TestParseCatalogJSON_EmptyObject(t *testing.T) {
	cat, err := runner.ParseCatalogJSON([]byte(`{}`))
	if err != nil {
		t.Fatalf("parse error on empty: %v", err)
	}
	if len(cat) != 0 {
		t.Errorf("expected empty catalog, got %v", cat)
	}
}

func TestParseCatalogJSON_MalformedReturnsError(t *testing.T) {
	_, err := runner.ParseCatalogJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestParseCatalogJSON_NoMCPServersIsEmptySlice(t *testing.T) {
	// Modules without MCP servers (e.g. `apple`, `build`) should parse
	// with an empty/nil MCPServers, not error.
	raw := []byte(`{
		"apple": {
			"description": "Swift toolchain",
			"mcpServers": [],
			"sizeMb": 900
		}
	}`)
	cat, err := runner.ParseCatalogJSON(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(cat["apple"].MCPServers) != 0 {
		t.Errorf("apple: expected empty mcpServers, got %v", cat["apple"].MCPServers)
	}
}

// TestCatalogNames returns a sorted list of module names — used by the
// `devcell modules list` command and the validation error messages.
func TestCatalogNames_Sorted(t *testing.T) {
	cat := runner.Catalog{
		"zebra":       {Description: "z"},
		"apple":       {Description: "a"},
		"yahoo-fin":   {Description: "y"},
		"electronics": {Description: "e"},
	}
	got := cat.Names()
	want := []string{"apple", "electronics", "yahoo-fin", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() not sorted alphabetically:\n got: %v\nwant: %v", got, want)
	}
}
