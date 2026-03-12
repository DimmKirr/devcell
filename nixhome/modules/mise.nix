# mise.nix — mise runtime version manager (replaces asdf)
# Each language module (go.nix, node.nix, infra.nix) declares tools via
# devcell.mise.tools.<name> = "<version>". This module collects them into
# a single ~/.tool-versions file and generates global mise config.
{ pkgs, config, lib, ... }:
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
    home.packages = [ pkgs.mise ];

    home.file.".config/mise/config.toml" = lib.mkIf (cfg.tools != {}) {
      text = ''
        [settings]
        idiomatic_version_file = true
        idiomatic_version_file_enable_tools = ["node", "go"]
        trusted_config_paths = ["/"]
      '';
    };

    home.file.".tool-versions" = lib.mkIf (cfg.tools != {}) {
      text = toolVersionsContent + "\n";
    };

    home.file.".default-npm-packages" = lib.mkIf (cfg.defaultNpmPackages != []) {
      text = lib.concatStringsSep "\n" cfg.defaultNpmPackages + "\n";
    };
  };
}
