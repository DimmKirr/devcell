// managed_mcp_staging_test.go — L1 wiring checks that pure (nix2container)
// images stage nix-managed MCP configs to /etc/<agent>/ at image-build time.
//
// Failure mode this test pins:
//
//	Each LLM module (claude.nix, codex.nix, opencode.nix, gemini.nix) has a
//	`home.activation.setupManaged<Agent>` script that runs
//	`sudo cp ${cfg} /etc/<agent>/nix-mcp-servers.<ext>` at home-manager
//	switch time. The pure (nix2container) image build SKIPS home-manager
//	activation entirely — only `home.file` content is materialized via
//	`cp -aL home-files/. ...`. Without an explicit staging step in
//	`nixhome/packages/image.nix homeRoot`, those config files never reach
//	/etc/<agent>/ in pure cells. Then fragments/30-*.sh short-circuits at
//	`[ -f "$nix_file" ] || return 0` and the user's ~/.claude.json
//	(or codex/opencode/gemini equivalents) never receive the nix-declared
//	MCP servers — e.g., the inkscape-mcp INKS_WORKSPACE override doesn't
//	reach the running MCP subprocess, and it falls back to upstream's
//	cwd-relative `inkspace/` litter directory.

package container_test

import (
	"strings"
	"testing"
)

// TestImageNix_StagesAllAgentMcpConfigs asserts the pure-image build stages
// the four agent MCP config files at the same /etc/<agent>/ paths the
// entrypoint fragments expect.
func TestImageNix_StagesAllAgentMcpConfigs(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	// Pin the staging targets. Match against the literal target paths in
	// `homeRoot` — these are exactly what fragments/30-*.sh read.
	wantTargets := []string{
		"/etc/claude-code/nix-mcp-servers.json", // fragments/30-claude.sh:59
		"/etc/codex/nix-mcp-servers.toml",       // fragments/30-codex.sh:7
		"/etc/opencode/nix-mcp-servers.json",    // fragments/30-opencode.sh:57
		"/etc/opencode/nix-providers.json",      // fragments/30-opencode.sh:7
		"/etc/gemini/nix-mcp-servers.json",      // fragments/30-gemini.sh:11
	}

	for _, target := range wantTargets {
		if !strings.Contains(imgNix, target) {
			t.Errorf("nixhome/packages/image.nix doesn't stage %q — pure-image cells will have empty ~/.claude.json / codex / opencode / gemini MCP server lists at container start; nix-declared MCP servers (like inkscape-mcp's INKS_WORKSPACE override) won't reach the running agent processes",
				target)
		}
	}
}

// TestImageNix_StagesMcpConfigsFromExposedOptions asserts that the staging
// reads from the read-only nix options each LLM module exposes
// (devcell.managed{Claude,Codex,Opencode,Gemini}.nixMcpConfigFile) rather
// than duplicating the JSON/TOML generation logic. Catches the regression
// where image.nix would re-implement the per-agent transformer and drift
// from the canonical module's shape.
func TestImageNix_StagesMcpConfigsFromExposedOptions(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	wantOptionRefs := []string{
		"managedClaude.nixMcpConfigFile",
		"managedCodex.nixMcpConfigFile",
		"managedOpencode.nixMcpConfigFile",
		"managedOpencode.nixProvidersFile",
		"managedGemini.nixMcpConfigFile",
	}

	for _, ref := range wantOptionRefs {
		if !strings.Contains(imgNix, ref) {
			t.Errorf("image.nix doesn't read %q — staging should consume the read-only option exposed by the respective LLM module, not re-derive the config from devcell.managedMcp.servers (would duplicate the per-agent JSON/TOML shape transformers)", ref)
		}
	}
}

// TestGraphicsNix_InkscapeWorkspaceOverridden pins INKS_WORKSPACE to the
// project root ("./") so the agent can edit any SVG anywhere in the project,
// while still preventing the upstream default `inkspace` (a typo'd relative
// path) from resurfacing and creating a scratch directory in cwd.
func TestGraphicsNix_InkscapeWorkspaceOverridden(t *testing.T) {
	graphics := readNixhomeFile(t, "modules/graphics.nix")

	if !strings.Contains(graphics, `INKS_WORKSPACE = "./"`) {
		t.Fatal("modules/graphics.nix doesn't pin INKS_WORKSPACE to `\"./\"` — the workspace should be the project root so the agent can read/write SVGs anywhere in the project (path traversal outside the root is still blocked by the MCP server)")
	}
	if strings.Contains(graphics, `INKS_WORKSPACE = "inkspace"`) {
		t.Fatal("INKS_WORKSPACE must not be set to the upstream default `inkspace` — it's a cwd-relative scratch dir that would litter every project root")
	}
}

func TestCodexNix_StagesManagedServerRegistry(t *testing.T) {
	codex := readNixhomeFile(t, "modules/llm/codex.nix")
	fragment := readNixhomeFile(t, "modules/fragments/30-codex.sh")

	if !strings.Contains(codex, "devcellManagedServers = builtins.attrNames mcpCfg.servers") {
		t.Fatal("codex.nix must stage all managed server names, including HTTP-only entries, so Codex startup can remove stale generated MCP config")
	}
	if !strings.Contains(fragment, "managed_names = set(nix.get('devcellManagedServers', []))") {
		t.Fatal("30-codex.sh must consume devcellManagedServers when cleaning stale generated MCP entries")
	}
	if !strings.Contains(fragment, "resolve_env_value") || !strings.Contains(fragment, "codex_ready_servers") {
		t.Fatal("30-codex.sh must resolve ${VAR} env placeholders and skip Codex MCP servers with missing required env")
	}
}

func TestCredentialMcpServersDeclareRequiredEnv(t *testing.T) {
	checks := map[string][]string{
		"modules/project-management.nix": {
			`HUBSTAFF_REFRESH_TOKEN = "\${HUBSTAFF_REFRESH_TOKEN}"`,
			`N8N_API_URL = "\${N8N_API_URL}"`,
			`N8N_API_KEY = "\${N8N_API_KEY}"`,
		},
		"modules/travel.nix": {
			`GOOGLE_MAPS_API_KEY = "\${GOOGLE_MAPS_API_KEY}"`,
			`TRIPIT_USERNAME = "\${TRIPIT_USERNAME}"`,
			`TRIPIT_PASSWORD = "\${TRIPIT_PASSWORD}"`,
			`TRIPIT_CLIENT_ID = "\${TRIPIT_CLIENT_ID}"`,
			`TRIPIT_CLIENT_SECRET = "\${TRIPIT_CLIENT_SECRET}"`,
		},
		"modules/qa-tools.nix": {
			`MAILSLURP_API_KEY = "\${MAILSLURP_API_KEY}"`,
		},
		"modules/infra.nix": {
			`NOTION_TOKEN = "\${NOTION_API_KEY}"`,
		},
	}

	for relPath, wantSnippets := range checks {
		content := readNixhomeFile(t, relPath)
		for _, want := range wantSnippets {
			if !strings.Contains(content, want) {
				t.Fatalf("nixhome/%s is missing required credential env declaration %q", relPath, want)
			}
		}
	}
}
