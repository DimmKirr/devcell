package container_test

// kicad_mcp_test.go — tests for the kicad-mcp MCP server in the electronics profile.
// Run against the electronics image:
//
//	DEVCELL_TEST_IMAGE=ghcr.io/dimmkirr/devcell:latest-electronics go test -v -run TestKicadMcp ./...

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKicadMcp(t *testing.T) {
	c := startContainer(t, map[string]string{"HOST_USER": hostUser})

	// ── 1. Binary on PATH ─────────────────────────────────────────────────────
	t.Run("binary on PATH", func(t *testing.T) {
		out, code := asUser(t, c, "command -v kicad-mcp")
		if code != 0 {
			t.Fatalf("FAIL: kicad-mcp not on PATH (exit %d)", code)
		}
		t.Logf("PASS: %s", strings.TrimSpace(out))
	})

	// ── 2. Registered in nix-mcp-servers.json ────────────────────────────────
	t.Run("registered in nix-mcp-servers.json", func(t *testing.T) {
		raw, code := exec(t, c, []string{"cat", "/etc/claude-code/nix-mcp-servers.json"})
		if code != 0 {
			t.Fatalf("FAIL: could not read nix-mcp-servers.json (exit %d)", code)
		}
		var cfg struct {
			McpServers map[string]struct {
				Command string `json:"command"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("FAIL: invalid JSON: %v\n%s", err, raw)
		}
		entry, ok := cfg.McpServers["kicad-mcp"]
		if !ok {
			keys := make([]string, 0, len(cfg.McpServers))
			for k := range cfg.McpServers {
				keys = append(keys, k)
			}
			t.Fatalf("FAIL: kicad-mcp missing from nix-mcp-servers.json; present: [%s]",
				strings.Join(keys, ", "))
		}
		if entry.Command != "kicad-mcp" {
			t.Errorf("FAIL: expected command %q, got %q", "kicad-mcp", entry.Command)
		} else {
			t.Logf("PASS: kicad-mcp entry present, command=%s", entry.Command)
		}
	})

	// ── 3. MCP protocol: tools/list returns expected tools ────────────────────
	// Python mcp SDK uses Content-Length framing (LSP-style), not newline-delimited JSON.
	t.Run("MCP protocol responds with tools", func(t *testing.T) {
		out, code := asUser(t, c, `
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}'
NOTIF='{"jsonrpc":"2.0","method":"notifications/initialized"}'
LIST='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
{
    printf "%s\n%s\n%s\n" "$INIT" "$NOTIF" "$LIST"
    sleep 5
} | timeout 15 kicad-mcp 2>/dev/null; true
`)
		// exit 124 = timeout (server didn't exit on EOF) — responses already written
		if code != 0 && code != 124 {
			t.Fatalf("FAIL: kicad-mcp exited %d:\n%s", code, out)
		}
		if !strings.Contains(out, "list_projects") {
			t.Errorf("FAIL: tools/list response missing list_projects (exit %d):\n%s", code, out)
		} else {
			t.Logf("PASS: kicad-mcp responded with tools including list_projects (exit %d)", code)
		}
	})
}
