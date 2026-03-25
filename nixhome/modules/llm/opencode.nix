# opencode.nix — OpenCode provider configs and MCP server staging.
# Merged from managed-opencode.nix + OpenCode parts of managed-mcp.nix.
{
  pkgs,
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
  stdioServers = lib.filterAttrs (_: s: (s.type or "stdio") == "stdio") mcpCfg.servers;

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

  hasServers = mcpCfg.servers != {};
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
  };

  config = {
    home.packages = with pkgs; [
      opencode # AI coding agent for terminal
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
        export PATH="/usr/bin:/bin:$PATH"
        $DRY_RUN_CMD sudo mkdir -p /etc/opencode
        ${lib.optionalString hasProviders ''
          $DRY_RUN_CMD sudo cp ${providersFile} /etc/opencode/nix-providers.json
        ''}
        ${lib.optionalString hasServers ''
          $DRY_RUN_CMD sudo cp ${openCodeConfig} /etc/opencode/nix-mcp-servers.json
        ''}
      ''
    );
  };
}
