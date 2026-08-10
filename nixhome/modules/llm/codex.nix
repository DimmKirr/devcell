# codex.nix — Codex CLI from npm + MCP server staging and entrypoint merge logic.
{
  pkgs,
  pkgsEdge,
  lib,
  config,
  ...
}:
let
  mcpCfg = config.devcell.managedMcp;

  toml = pkgs.formats.toml { };

  # ── Codex CLI from npm ──────────────────────────────────────────────
  # Fetches the platform-specific prebuilt binary directly from the npm
  # registry. The npm package ships a Node.js shim that spawns a static
  # musl-linked Rust binary — we skip the shim and install the binary.
  codexVersion = "0.144.4";
  codexPlatforms = {
    "aarch64-linux" = {
      triple = "aarch64-unknown-linux-musl";
      url = "https://registry.npmjs.org/@openai/codex/-/codex-${codexVersion}-linux-arm64.tgz";
      hash = "sha256-OEYcdpzpXnNKQgC7Xnqsi76KgFJgPWSOrkr6omrAZqc=";
    };
    "x86_64-linux" = {
      triple = "x86_64-unknown-linux-musl";
      url = "https://registry.npmjs.org/@openai/codex/-/codex-${codexVersion}-linux-x64.tgz";
      hash = "sha256-mkpFMU6AtTxHYbgAZ+OmjCMC+akCYFm19U8i3sjzQyM=";
    };
  };
  codexPlatform = codexPlatforms.${pkgs.stdenv.hostPlatform.system} or null;

  codex = if codexPlatform != null then pkgs.stdenv.mkDerivation {
    pname = "codex";
    version = codexVersion;
    src = pkgs.fetchurl {
      url = codexPlatform.url;
      hash = codexPlatform.hash;
    };
    sourceRoot = ".";
    unpackPhase = ''
      tar xzf $src
    '';
    installPhase = ''
      local vendor="package/vendor/${codexPlatform.triple}"
      mkdir -p $out/bin $out/lib/codex
      cp $vendor/bin/codex          $out/bin/codex
      cp $vendor/bin/codex-code-mode-host $out/bin/codex-code-mode-host
      cp -r $vendor/codex-path      $out/lib/codex/codex-path
      cp -r $vendor/codex-resources $out/lib/codex/codex-resources
    '';
    dontFixup = true;
  } else null;

  # Only stdio servers — Codex doesn't support HTTP transport.
  # Also skip servers explicitly disabled (enabled = false). Default: enabled.
  stdioServers = lib.filterAttrs (
    _: s: (s.type or "stdio") == "stdio" && (s.enabled or true)
  ) mcpCfg.servers;

  toCodexServer =
    _: s:
    {
      command = s.command;
      args = s.args or [ ];
    }
    // lib.optionalAttrs ((s.env or { }) != { }) { env = s.env; };

  codexConfig = toml.generate "codex-nix-mcp-servers.toml" {
    backupBeforeMerge = mcpCfg.backupBeforeMerge;
    devcellManagedServers = builtins.attrNames mcpCfg.servers;
    mcp_servers = lib.mapAttrs toCodexServer stdioServers;
  };

  hasServers = stdioServers != { };
in
{
  options.devcell.managedCodex = {
    nixMcpConfigFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = if hasServers then codexConfig else null;
      internal = true;
      readOnly = true;
      description = "Nix-store path of the generated Codex MCP servers TOML (null when no servers declared).";
    };
  };

  config = {
    home.packages = lib.optional (codex != null) codex;

    # Always generate the Codex merge fragment (self-guards at runtime)
    home.file.".config/devcell/entrypoint.d/30-codex.sh" = {
      executable = true;
      source = ../fragments/30-codex.sh;
    };

    # Stage Codex MCP config when servers are defined
    home.activation.setupManagedCodex = lib.mkIf hasServers (
      lib.hm.dag.entryAfter [ "writeBoundary" ] ''
        # /run/wrappers/bin: NixOS keeps the sudo setuid wrapper there only.
        export PATH="/usr/bin:/bin:/run/wrappers/bin:$PATH"
        if command -v sudo >/dev/null 2>&1; then
          $DRY_RUN_CMD sudo mkdir -p /etc/codex
          $DRY_RUN_CMD sudo rm -f /etc/codex/managed_config.toml
          $DRY_RUN_CMD sudo cp ${codexConfig} /etc/codex/nix-mcp-servers.toml
        else
          echo "setupManagedCodex: sudo not available — skipping /etc/codex staging"
        fi
      ''
    );
  };
}
