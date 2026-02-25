# managed-mcp.nix — aggregates MCP server configs contributed by any module
# and generates system-level config files for all supported LLM CLI tools.
#
# Usage in any module:
#   devcell.managedMcp.servers.my-server = {
#     command = "my-mcp-server";       # executable name/path (string)
#     args    = ["--flag" "value"];    # CLI args (list of strings)
#     env     = { KEY = "value"; };   # environment variables (attrset)
#   };
#
# The nix module system merges all contributions automatically.
# Generates (when servers != {}):
#   /etc/claude-code/nix-mcp-servers.json — staging file; entrypoint.sh merges
#                                           into ~/.claude.json at container start
#                                           (additive — users keep their own servers)
#   /etc/opencode/nix-mcp-servers.json    — staging; entrypoint merges into ~/.opencode.json
#   /etc/codex/nix-mcp-servers.toml       — staging; entrypoint merges into ~/.codex/config.toml
{
  pkgs,
  lib,
  config,
  ...
}: let
  servers = config.devcell.managedMcp.servers;

  # ── Format converters ────────────────────────────────────────────────────────

  # Claude Code: { type, command, args, env }
  toClaudeServer = _: s: {
    type = s.type or "stdio";
    command = s.command;
    args = s.args or [];
    env = s.env or {};
  };

  # OpenCode: { type, command (merged array), environment }
  toOpenCodeServer = _: s:
    {
      type = "local";
      command = [s.command] ++ (s.args or []);
    }
    // lib.optionalAttrs ((s.env or {}) != {}) {environment = s.env;};

  # Codex CLI (TOML): { command, args, env-section }
  # No type field — inferred from presence of command vs url.
  toCodexServer = _: s:
    {
      command = s.command;
      args = s.args or [];
    }
    // lib.optionalAttrs ((s.env or {}) != {}) {env = s.env;};

  # ── Config file derivations ──────────────────────────────────────────────────

  json = pkgs.formats.json {};
  toml = pkgs.formats.toml {};

  claudeConfig = json.generate "claude-nix-mcp-servers.json" {
    backupBeforeMerge = config.devcell.managedMcp.backupBeforeMerge;
    mcpServers = lib.mapAttrs toClaudeServer servers;
  };

  openCodeConfig = json.generate "opencode-nix-mcp-servers.json" {
    backupBeforeMerge = config.devcell.managedMcp.backupBeforeMerge;
    mcp = lib.mapAttrs toOpenCodeServer servers;
  };

  codexConfig = toml.generate "codex-nix-mcp-servers.toml" {
    backupBeforeMerge = config.devcell.managedMcp.backupBeforeMerge;
    mcp_servers = lib.mapAttrs toCodexServer servers;
  };
in {
  options.devcell.managedMcp = {
    servers = lib.mkOption {
      type = lib.types.attrsOf lib.types.anything;
      default = {};
      description = "Canonical MCP server definitions. Each entry: { command, args?, env? }.";
    };
    backupBeforeMerge = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Whether entrypoint.sh should back up user config files before merging nix MCP servers (Claude, OpenCode, Codex).";
    };
  };

  config = lib.mkIf (servers != {}) {
    home.activation.setupManagedMcp = lib.hm.dag.entryAfter ["writeBoundary"] ''
      # sudo may not be in PATH when home-manager activation runs inside a Docker
      # build layer (restricted Nix-managed PATH). Add system bin dirs as fallback.
      export PATH="/usr/bin:/bin:$PATH"
      $DRY_RUN_CMD sudo mkdir -p /etc/claude-code /etc/opencode /etc/codex
      # Remove legacy files that had undesired exclusive-control or requirements semantics.
      $DRY_RUN_CMD sudo rm -f /etc/claude-code/managed-mcp.json
      $DRY_RUN_CMD sudo rm -f /etc/codex/managed_config.toml
      $DRY_RUN_CMD sudo cp ${claudeConfig} /etc/claude-code/nix-mcp-servers.json
      $DRY_RUN_CMD sudo cp ${openCodeConfig} /etc/opencode/nix-mcp-servers.json
      $DRY_RUN_CMD sudo cp ${codexConfig} /etc/codex/nix-mcp-servers.toml
    '';
  };
}
