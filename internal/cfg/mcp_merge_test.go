package cfg_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These tests exercise the actual jq expressions used in the MCP merge scripts
// (30-claude.sh, claude.nix activation, 30-opencode.sh) to verify that stale
// nix-managed servers are removed before adding the current stack's servers.

// claudeMergeJQ is the jq expression from 30-claude.sh and claude.nix activation.
const claudeMergeJQ = `
.[0] as $existing |
.[1].mcpServers as $nix |
(($existing.mcpServers // {}) | to_entries |
  map(select(.value.command == null or (.value.command | startswith("/opt/devcell/") | not))) |
  from_entries) as $cleaned |
$existing | .mcpServers = ($cleaned + ($nix // {}))
`

// opencodeMergeJQ is the jq expression from 30-opencode.sh.
const opencodeMergeJQ = `
.[0] as $existing |
.[1].mcp as $nix |
(($existing.mcp // {}) | to_entries |
  map(select(.value.command == null or (.value.command[0] == null) or (.value.command[0] | startswith("/opt/devcell/") | not))) |
  from_entries) as $cleaned |
$existing | .mcp = ($cleaned + ($nix // {}))
`

func runJQ(t *testing.T, expr string, files ...string) map[string]any {
	t.Helper()
	args := []string{"-s", expr}
	args = append(args, files...)
	cmd := exec.Command("jq", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jq failed: %v\noutput: %s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, out)
	}
	return result
}

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func serverCommand(servers map[string]any, name string) string {
	srv, ok := servers[name]
	if !ok {
		return ""
	}
	m := srv.(map[string]any)
	cmd, _ := m["command"].(string)
	return cmd
}

// TestClaudeMerge_RemovesStaleNixServers verifies that switching from ultimate
// (13 servers) to fullstack (5 servers) removes the 8 stale nix-managed entries.
func TestClaudeMerge_RemovesStaleNixServers(t *testing.T) {
	dir := t.TempDir()

	// Simulate existing ~/.claude.json after running ultimate stack
	existing := map[string]any{
		"mcpServers": map[string]any{
			"yahoo-finance": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/yahoo-finance-mcp",
				"args":    []string{},
			},
			"kicad-mcp": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/kicad-mcp",
				"args":    []string{},
			},
			"inkscape-mcp": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/inkscape-mcp",
				"args":    []string{},
			},
			"playwright": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/patchright-mcp-cell",
				"args":    []string{"--browser", "chromium"},
			},
			// User-defined server (not nix-managed)
			"my-custom-server": map[string]any{
				"command": "/usr/local/bin/my-server",
				"args":    []string{},
			},
		},
	}

	// Simulate nix-mcp-servers.json for fullstack (no kicad, inkscape, playwright)
	nixServers := map[string]any{
		"mcpServers": map[string]any{
			"yahoo-finance": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/yahoo-finance-mcp",
				"args":    []string{},
			},
		},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, claudeMergeJQ, existingFile, nixFile)

	servers := result["mcpServers"].(map[string]any)

	// yahoo-finance should remain (in new stack)
	if serverCommand(servers, "yahoo-finance") == "" {
		t.Error("yahoo-finance should be present")
	}

	// User server should be preserved
	if serverCommand(servers, "my-custom-server") != "/usr/local/bin/my-server" {
		t.Error("user-defined my-custom-server should be preserved")
	}

	// Stale nix servers should be removed
	for _, stale := range []string{"kicad-mcp", "inkscape-mcp", "playwright"} {
		if _, exists := servers[stale]; exists {
			t.Errorf("stale server %q should have been removed", stale)
		}
	}
}

// TestClaudeMerge_PreservesUserServers verifies that servers without the
// /opt/devcell/ prefix survive the merge untouched.
func TestClaudeMerge_PreservesUserServers(t *testing.T) {
	dir := t.TempDir()

	existing := map[string]any{
		"mcpServers": map[string]any{
			"user-mcp": map[string]any{
				"command": "my-local-mcp",
				"args":    []string{"--port", "8080"},
			},
			"remote-mcp": map[string]any{
				"command": "/home/user/bin/remote-mcp",
			},
		},
	}

	nixServers := map[string]any{
		"mcpServers": map[string]any{
			"nixos": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/mcp-nixos",
				"args":    []string{},
			},
		},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, claudeMergeJQ, existingFile, nixFile)
	servers := result["mcpServers"].(map[string]any)

	if serverCommand(servers, "user-mcp") != "my-local-mcp" {
		t.Error("user-mcp should be preserved")
	}
	if serverCommand(servers, "remote-mcp") != "/home/user/bin/remote-mcp" {
		t.Error("remote-mcp should be preserved")
	}
	if serverCommand(servers, "nixos") == "" {
		t.Error("nixos should be added from nix servers")
	}
}

// TestClaudeMerge_PreservesHTTPServers verifies that HTTP-type servers
// (no command field) survive the cleanup filter.
func TestClaudeMerge_PreservesHTTPServers(t *testing.T) {
	dir := t.TempDir()

	existing := map[string]any{
		"mcpServers": map[string]any{
			"linear-server": map[string]any{
				"type": "http",
				"url":  "https://mcp.linear.app/mcp",
			},
			"old-nix-server": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/old-tool",
				"args":    []string{},
			},
		},
	}

	nixServers := map[string]any{
		"mcpServers": map[string]any{},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, claudeMergeJQ, existingFile, nixFile)
	servers := result["mcpServers"].(map[string]any)

	if _, exists := servers["linear-server"]; !exists {
		t.Error("HTTP server linear-server should be preserved (no command field)")
	}
	if _, exists := servers["old-nix-server"]; exists {
		t.Error("old-nix-server should have been removed")
	}
}

// TestClaudeMerge_EmptyExisting verifies merge works when starting fresh.
func TestClaudeMerge_EmptyExisting(t *testing.T) {
	dir := t.TempDir()

	existing := map[string]any{}
	nixServers := map[string]any{
		"mcpServers": map[string]any{
			"yahoo-finance": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/yahoo-finance-mcp",
				"args":    []string{},
			},
		},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, claudeMergeJQ, existingFile, nixFile)
	servers := result["mcpServers"].(map[string]any)

	if serverCommand(servers, "yahoo-finance") == "" {
		t.Error("yahoo-finance should be added to empty config")
	}
}

// TestClaudeMerge_PreservesOtherFields verifies non-mcpServers fields survive.
func TestClaudeMerge_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()

	existing := map[string]any{
		"primaryApiKey":          "sk-ant-xxx",
		"hasCompletedOnboarding": true,
		"mcpServers": map[string]any{
			"stale": map[string]any{
				"command": "/opt/devcell/.local/state/nix/profiles/profile/bin/stale-tool",
			},
		},
	}

	nixServers := map[string]any{
		"mcpServers": map[string]any{},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, claudeMergeJQ, existingFile, nixFile)

	if result["primaryApiKey"] != "sk-ant-xxx" {
		t.Error("primaryApiKey should be preserved")
	}
	if result["hasCompletedOnboarding"] != true {
		t.Error("hasCompletedOnboarding should be preserved")
	}
}

// TestOpencodeMerge_RemovesStaleNixServers tests the opencode jq expression
// which uses array-style command fields.
func TestOpencodeMerge_RemovesStaleNixServers(t *testing.T) {
	dir := t.TempDir()

	existing := map[string]any{
		"mcp": map[string]any{
			"yahoo-finance": map[string]any{
				"type":    "local",
				"command": []string{"/opt/devcell/.local/state/nix/profiles/profile/bin/yahoo-finance-mcp"},
			},
			"kicad-mcp": map[string]any{
				"type":    "local",
				"command": []string{"/opt/devcell/.local/state/nix/profiles/profile/bin/kicad-mcp"},
			},
			"user-tool": map[string]any{
				"type":    "local",
				"command": []string{"/usr/bin/my-tool", "--flag"},
			},
		},
	}

	nixServers := map[string]any{
		"mcp": map[string]any{
			"yahoo-finance": map[string]any{
				"type":    "local",
				"command": []string{"/opt/devcell/.local/state/nix/profiles/profile/bin/yahoo-finance-mcp"},
			},
		},
	}

	existingFile := writeJSON(t, dir, "existing.json", existing)
	nixFile := writeJSON(t, dir, "nix.json", nixServers)

	result := runJQ(t, opencodeMergeJQ, existingFile, nixFile)
	servers := result["mcp"].(map[string]any)

	if _, exists := servers["yahoo-finance"]; !exists {
		t.Error("yahoo-finance should be present")
	}
	if _, exists := servers["user-tool"]; !exists {
		t.Error("user-tool should be preserved")
	}
	if _, exists := servers["kicad-mcp"]; exists {
		t.Error("stale kicad-mcp should have been removed")
	}
}
