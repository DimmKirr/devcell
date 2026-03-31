# codex.nix — Codex CLI MCP server staging and entrypoint merge logic.
# Extracted from managed-mcp.nix.
{
  pkgs,
  pkgsEdge,
  lib,
  config,
  ...
}: let
  mcpCfg = config.devcell.managedMcp;

  toml = pkgs.formats.toml {};

  # Only stdio servers — Codex doesn't support HTTP transport.
  stdioServers = lib.filterAttrs (_: s: (s.type or "stdio") == "stdio") mcpCfg.servers;

  toCodexServer = _: s:
    {
      command = s.command;
      args = s.args or [];
    }
    // lib.optionalAttrs ((s.env or {}) != {}) {env = s.env;};

  codexConfig = toml.generate "codex-nix-mcp-servers.toml" {
    backupBeforeMerge = mcpCfg.backupBeforeMerge;
    mcp_servers = lib.mapAttrs toCodexServer stdioServers;
  };

  hasServers = mcpCfg.servers != {};
in {
  config = {
    home.packages = [ pkgsEdge.codex ];

    # Always generate the Codex merge fragment (self-guards at runtime)
    home.file.".config/devcell/entrypoint.d/30-codex.sh" = {
      executable = true;
      source = ../fragments/30-codex.sh;
    };

    # Stage Codex MCP config when servers are defined
    home.activation.setupManagedCodex = lib.mkIf hasServers (
      lib.hm.dag.entryAfter ["writeBoundary"] ''
        export PATH="/usr/bin:/bin:$PATH"
        $DRY_RUN_CMD sudo mkdir -p /etc/codex
        $DRY_RUN_CMD sudo rm -f /etc/codex/managed_config.toml
        $DRY_RUN_CMD sudo cp ${codexConfig} /etc/codex/nix-mcp-servers.toml
      ''
    );
  };
}
