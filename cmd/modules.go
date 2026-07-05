package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/spf13/cobra"
)

var modulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "Inspect the DevCell modules catalog",
}

var modulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available modules (name, size, description, MCP servers)",
	Long: `Lists every module from the DevCell catalog: each can be added to
your .devcell.toml as:

  [cell]
  modules = ["electronics", "yahoo-finance"]

Catalog source: the active nixhome flake's devcellModules output.`,
	RunE: runModulesList,
}

func init() {
	modulesCmd.AddCommand(modulesListCmd)
}

func runModulesList(cmd *cobra.Command, args []string) error {
	flakeRef := resolveModulesFlakeRef()

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	cat, err := runner.ReadCatalogFromFlake(ctx, flakeRef)
	if err != nil {
		return fmt.Errorf("read catalog from %s: %w\n\nHint: make sure the nixhome flake is reachable and has a `devcellModules` output. See https://devcell.sh/docs/modules", flakeRef, err)
	}
	fmt.Print(formatCatalogList(cat))
	return nil
}

// resolveModulesFlakeRef returns the flake reference to read the catalog from.
// Precedence: DEVCELL_NIXHOME_PATH env > current working dir's nixhome > bundled.
func resolveModulesFlakeRef() string {
	if v := os.Getenv("DEVCELL_NIXHOME_PATH"); v != "" {
		return "path:" + v
	}
	// Default: assume CWD has a nixhome/ subdir (the source checkout).
	// Production CLI will substitute the bundled flake pin via runner config.
	if _, err := os.Stat("./nixhome/flake.nix"); err == nil {
		return "path:./nixhome"
	}
	return runner.UpstreamFlakeRefNoVersion()
}

// formatCatalogList renders the catalog as a human-readable table.
// Pure function — fed a Catalog, returns a string. Easy to test.
func formatCatalogList(cat runner.Catalog) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODULE\tSIZE\tMCP SERVERS\tDESCRIPTION")
	for _, name := range cat.Names() {
		m := cat[name]
		size := fmt.Sprintf("%d MB", m.SizeMB)
		mcps := "—"
		if len(m.MCPServers) > 0 {
			mcps = strings.Join(m.MCPServers, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, size, mcps, m.Description)
	}
	_ = tw.Flush()
	return b.String()
}
