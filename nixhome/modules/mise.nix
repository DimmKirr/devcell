# mise.nix — mise runtime version manager (replaces asdf)
# Each language module (go.nix, node.nix, infra.nix) declares tools via
# devcell.mise.tools.<name> = "<version>". This module collects them into
# a single ~/.tool-versions file and generates global mise config.
{ pkgs, pkgsEdge, config, lib, ... }:
let
  cfg = config.devcell.mise;
  toolVersionsContent = lib.concatStringsSep "\n"
    (lib.mapAttrsToList (name: version: "${name} ${version}") cfg.tools);
in {
  options.devcell.mise = {
    tools = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = {};
      description = "Tool name to version mapping for .tool-versions";
    };
    defaultNpmPackages = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [];
      description = "NPM packages auto-installed after Node.js (written to ~/.default-npm-packages)";
    };
  };

  config = {
    # pkgsEdge: shared install dirs (MISE_SHARED_INSTALL_DIRS) need mise
    # ≥2026.3.9 (jdx/mise#8581); nixpkgs stable/unstable lag behind.
    home.packages = [ pkgsEdge.mise ];

    # User shims only. Baked tools are resolved natively by mise through
    # MISE_SHARED_INSTALL_DIRS (read-only baked install dir, set as image
    # env) — user installs in ~/.local/share/mise take precedence, like PATH.
    # Replaces the two-level shim PATH + cross-bind symlinks (CELL-85/294).
    home.sessionPath = [
      "${config.home.homeDirectory}/.local/share/mise/shims"
    ];

    home.file.".config/mise/config.toml" = lib.mkIf (cfg.tools != {}) {
      text = ''
        [settings]
        idiomatic_version_file = true
        idiomatic_version_file_enable_tools = ["node", "go"]
        trusted_config_paths = ["/"]

        [tools]
      '' + lib.concatStringsSep "\n"
        (lib.mapAttrsToList (name: version: "${name} = \"${version}\"") cfg.tools)
      + "\n";
    };

    # .tool-versions is written to /etc/devcell/ (not home.file) to avoid
    # nix creating a dangling symlink at $HOME. The entrypoint fragment
    # copies it to $HOME at runtime; build-time mise install reads it
    # from /opt/devcell/ via the activation-generated copy.
    home.activation.writeToolVersions = lib.mkIf (cfg.tools != {}) (
      lib.hm.dag.entryAfter ["writeBoundary"] ''
        export PATH="/usr/bin:/bin:$PATH"
        $DRY_RUN_CMD mkdir -p /etc/devcell
        echo ${lib.escapeShellArg (toolVersionsContent + "\n")} | $DRY_RUN_CMD tee /etc/devcell/tool-versions > /dev/null
        $DRY_RUN_CMD cp /etc/devcell/tool-versions "$HOME/.tool-versions" 2>/dev/null || true
      ''
    );

    home.file.".default-npm-packages" = lib.mkIf (cfg.defaultNpmPackages != []) {
      text = lib.concatStringsSep "\n" cfg.defaultNpmPackages + "\n";
    };

    # ── Entrypoint fragment: mise setup ──────────────────────────────────────
    home.file.".config/devcell/entrypoint.d/10-mise.sh" = {
      executable = true;
      source = ./fragments/10-mise.sh;
    };
  };
}
