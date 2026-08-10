# opencode.nix — OpenCode provider configs and MCP server staging.
# Merged from managed-opencode.nix + OpenCode parts of managed-mcp.nix.
{
  pkgs,
  pkgsEdge,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.managedOpencode;
  mcpCfg = config.devcell.managedMcp;

  json = pkgs.formats.json {};

  providersFile = json.generate "opencode-nix-providers.json" {
    provider = cfg.providers;
  };

  hasProviders = cfg.providers != {};

  # OpenCode MCP config derivation (from mcp.nix servers)
  # Only stdio servers — OpenCode doesn't support HTTP transport.
  # Also skip servers explicitly disabled (enabled = false). Default: enabled.
  stdioServers = lib.filterAttrs (
    _: s: (s.type or "stdio") == "stdio" && (s.enabled or true)
  ) mcpCfg.servers;

  toOpenCodeServer = _: s:
    {
      type = "local";
      command = [s.command] ++ (s.args or []);
    }
    // lib.optionalAttrs ((s.env or {}) != {}) {environment = s.env;};

  openCodeConfig = json.generate "opencode-nix-mcp-servers.json" {
    backupBeforeMerge = mcpCfg.backupBeforeMerge;
    mcp = lib.mapAttrs toOpenCodeServer stdioServers;
  };

  hasServers = stdioServers != {};
in {
  options.devcell.managedOpencode = {
    providers = lib.mkOption {
      type = lib.types.attrsOf lib.types.anything;
      default = {};
      description = ''
        OpenCode provider configs merged into ~/opencode.json at container start.
        Each key is a provider ID; the value is the provider config object.
        Providers are only injected if the key is not already present in the
        user's existing ~/opencode.json.
      '';
    };

    # Read-only — expose the generated config derivations so the pure
    # (nix2container) image builder can stage them directly to /etc/opencode/
    # at image-build time. Activation-script-based staging (line ~85 below)
    # doesn't run on pure images because home-manager activation is skipped.
    nixMcpConfigFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = if hasServers then openCodeConfig else null;
      internal = true;
      readOnly = true;
      description = "Nix-store path of the generated OpenCode MCP servers JSON (null when no servers declared).";
    };
    nixProvidersFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = if hasProviders then providersFile else null;
      internal = true;
      readOnly = true;
      description = "Nix-store path of the generated OpenCode providers JSON (null when no providers declared).";
    };
  };

  config = {
    home.packages = with pkgs; [
      pkgsEdge.opencode # AI coding agent for terminal (edge for latest)
    ];

    # ── Default OpenCode provider config ─────────────────────────────────
    devcell.managedOpencode.providers = {
      lmstudio = {
        npm = "@ai-sdk/openai-compatible";
        name = "LM Studio (local)";
        options.baseURL = "http://127.0.0.1:1234/v1";
        models = {
          "google/gemma-3n-e4b".name = "Gemma 3n-e4b (local)";
          "zai-org_glm-4.7-flash".name = "GLM-4.7 (local)";
        };
      };
    };

    # Always generate the fragment (self-guards at runtime)
    home.file.".config/devcell/entrypoint.d/30-opencode.sh" = {
      executable = true;
      source = ../fragments/30-opencode.sh;
    };

    # Stage providers + MCP servers when configured
    home.activation.setupManagedOpencode = lib.mkIf (hasProviders || hasServers) (
      lib.hm.dag.entryAfter ["writeBoundary"] ''
        # /run/wrappers/bin: NixOS keeps the sudo setuid wrapper there only.
        export PATH="/usr/bin:/bin:/run/wrappers/bin:$PATH"
        if command -v sudo >/dev/null 2>&1; then
          $DRY_RUN_CMD sudo mkdir -p /etc/opencode
          ${lib.optionalString hasProviders ''
            $DRY_RUN_CMD sudo cp ${providersFile} /etc/opencode/nix-providers.json
          ''}
          ${lib.optionalString hasServers ''
            $DRY_RUN_CMD sudo cp ${openCodeConfig} /etc/opencode/nix-mcp-servers.json
          ''}
        else
          echo "setupManagedOpencode: sudo not available — skipping /etc/opencode staging"
        fi
      ''
    );
  };
}
