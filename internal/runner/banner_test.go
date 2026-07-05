package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestFormatMCPLoadedBanner_NixOnly: cell starts with only nix-managed MCPs.
func TestFormatMCPLoadedBanner_NixOnly(t *testing.T) {
	got := runner.FormatMCPLoadedBanner(
		[]string{"patchright", "opentofu"},
		nil,
	)
	for _, want := range []string{"patchright", "opentofu", "MCP"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in banner, got: %s", want, got)
		}
	}
}

// TestFormatMCPLoadedBanner_NixAndUserSplit: when user has their own MCPs
// from ~/.claude/mcp.json, banner must show "nix" and "yours" separately.
func TestFormatMCPLoadedBanner_NixAndUserSplit(t *testing.T) {
	got := runner.FormatMCPLoadedBanner(
		[]string{"patchright", "opentofu"},
		[]string{"linear", "github"},
	)
	// Banner must distinguish the two sources so user knows their config carried over.
	if !strings.Contains(strings.ToLower(got), "nix") {
		t.Errorf("missing 'nix' label, got: %s", got)
	}
	if !strings.Contains(strings.ToLower(got), "yours") &&
		!strings.Contains(strings.ToLower(got), "user") {
		t.Errorf("missing 'yours'/'user' label, got: %s", got)
	}
	for _, want := range []string{"patchright", "opentofu", "linear", "github"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in banner, got: %s", want, got)
		}
	}
}

// TestFormatMCPLoadedBanner_NamesAreSorted: stable output for snapshot/diff.
func TestFormatMCPLoadedBanner_NamesAreSorted(t *testing.T) {
	got := runner.FormatMCPLoadedBanner(
		[]string{"zebra", "apple", "kicad"},
		nil,
	)
	idxA := strings.Index(got, "apple")
	idxK := strings.Index(got, "kicad")
	idxZ := strings.Index(got, "zebra")
	if !(idxA < idxK && idxK < idxZ) {
		t.Errorf("names not sorted (apple=%d kicad=%d zebra=%d):\n%s",
			idxA, idxK, idxZ, got)
	}
}

// TestFormatMCPLoadedBanner_EmptyBoth: no MCPs loaded at all — should print
// something honest rather than silently hide the state.
func TestFormatMCPLoadedBanner_EmptyBoth(t *testing.T) {
	got := runner.FormatMCPLoadedBanner(nil, nil)
	if got == "" {
		t.Errorf("empty MCP set should still produce a banner line so user knows what's loaded (or not)")
	}
}

// TestFormatMCPLoadedBanner_CountsShown: banner should include counts so users
// can grep for them ("3 nix + 2 yours") in CI logs.
func TestFormatMCPLoadedBanner_CountsShown(t *testing.T) {
	got := runner.FormatMCPLoadedBanner(
		[]string{"a", "b", "c"},
		[]string{"x", "y"},
	)
	if !strings.Contains(got, "3") {
		t.Errorf("expected count '3' for nix MCPs, got: %s", got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("expected count '2' for user MCPs, got: %s", got)
	}
}
