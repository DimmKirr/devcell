package runner

import (
	"fmt"
	"sort"
	"strings"
)

// FormatMCPLoadedBanner returns a single-line banner the CLI prints after the
// cell starts but before handing off to the agent UI. Its job is to prove to
// the user that:
//
//  1. Their `~/.claude/mcp.json` MCPs carried over into the cell.
//  2. The nix-managed (DevCell-bundled) MCPs are wired in.
//
// Format example:
//
//	✓ MCP loaded — nix: opentofu, patchright (2)   yours: github, linear (2)
//
// Both lists are sorted alphabetically for a stable, scannable output.
// If neither slice has entries, the banner reports the empty state honestly
// so the user can spot a config-mount failure immediately.
func FormatMCPLoadedBanner(nixMCPs, userMCPs []string) string {
	// Sort defensively — the caller may pass map-iteration order.
	nixSorted := sortedCopy(nixMCPs)
	userSorted := sortedCopy(userMCPs)

	var b strings.Builder
	b.WriteString("✓ MCP loaded — ")

	// nix segment — always present, even if zero.
	b.WriteString(fmt.Sprintf("nix: %s (%d)",
		joinOrPlaceholder(nixSorted, "—"),
		len(nixSorted),
	))

	// yours segment — always present so user knows their config was checked.
	b.WriteString("   yours: ")
	b.WriteString(fmt.Sprintf("%s (%d)",
		joinOrPlaceholder(userSorted, "—"),
		len(userSorted),
	))

	return b.String()
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func joinOrPlaceholder(s []string, placeholder string) string {
	if len(s) == 0 {
		return placeholder
	}
	return strings.Join(s, ", ")
}
