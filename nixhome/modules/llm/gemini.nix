# gemini.nix — Gemini CLI MCP server staging and entrypoint merge logic.
# Mirrors codex.nix; emits Claude-shape JSON instead of TOML because Gemini's
# ~/.gemini/settings.json holds `mcpServers` at top level (same shape as
# Claude's ~/.claude.json).
{
  pkgs,
  pkgsEdge,
  lib,
  config,
  ...
}: let
  mcpCfg = config.devcell.managedMcp;

  json = pkgs.formats.json {};

  # Only stdio servers — Gemini stdio MCP support is the established path.
  # Skip servers explicitly disabled (enabled = false). Default: enabled.
  stdioServers = lib.filterAttrs (
    _: s: (s.type or "stdio") == "stdio" && (s.enabled or true)
  ) mcpCfg.servers;

  toGeminiServer = _: s:
    {
      command = s.command;
      args = s.args or [];
    }
    // lib.optionalAttrs ((s.env or {}) != {}) {env = s.env;};

  geminiConfig = json.generate "gemini-nix-mcp-servers.json" {
    backupBeforeMerge = mcpCfg.backupBeforeMerge;
    mcpServers = lib.mapAttrs toGeminiServer stdioServers;
  };

  hasServers = stdioServers != {};
in {
  options.devcell.managedGemini = {
    # Read-only — exposes the generated config derivation so the pure
    # (nix2container) image builder can stage it directly to /etc/gemini/ at
    # image-build time. Activation-script-based staging (line ~50 below)
    # doesn't run on pure images because home-manager activation is skipped.
    nixMcpConfigFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = if hasServers then geminiConfig else null;
      internal = true;
      readOnly = true;
      description = "Nix-store path of the generated Gemini MCP servers JSON (null when no servers declared).";
    };
  };

  config = {
    home.packages = [pkgsEdge.gemini-cli];

    # Always generate the Gemini merge fragment (self-guards at runtime)
    home.file.".config/devcell/entrypoint.d/30-gemini.sh" = {
      executable = true;
      source = ../fragments/30-gemini.sh;
    };

    # Stage Gemini MCP config when servers are defined
    home.activation.setupManagedGemini = lib.mkIf hasServers (
      lib.hm.dag.entryAfter ["writeBoundary"] ''
        # /run/wrappers/bin: NixOS keeps the sudo setuid wrapper there only.
        export PATH="/usr/bin:/bin:/run/wrappers/bin:$PATH"
        if command -v sudo >/dev/null 2>&1; then
          $DRY_RUN_CMD sudo mkdir -p /etc/gemini
          $DRY_RUN_CMD sudo cp ${geminiConfig} /etc/gemini/nix-mcp-servers.json
        else
          echo "setupManagedGemini: sudo not available — skipping /etc/gemini staging"
        fi
      ''
    );
  };
}
