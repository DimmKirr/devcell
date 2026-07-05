# nixos.nix — Nix/NixOS development tools and MCP server
{
  pkgs,
  config,
  lib,
  mcp-nixos,
  ...
}: let
  cfg = config.devcell.modules.nixos;
  bin = config.devcell.managedMcp.nixBinPrefix;
  # mcpPkg disabled — upstream tests broken (utensils/mcp-nixos#154)
  # mcpPkg = mcp-nixos.packages.${pkgs.system}.default;
in {
  options.devcell.modules.nixos = {
    enable = lib.mkEnableOption "Nix/NixOS dev utilities (nix-tree, nixfmt, deadnix, statix, ...)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Nix dev tooling: nix-tree, nix-diff, nixfmt, deadnix, statix, nom";
        mcpServers = [ ];  # mcp-nixos disabled upstream
        sizeMb = 30;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      # mcpPkg
      nix-tree            # visualise nix store dependencies
      nix-diff            # diff nix derivations
      nix-prefetch-github # compute sha256 for fetchFromGitHub (use: nix-prefetch-github owner repo)
      nixfmt-rfc-style    # official nix formatter, nixpkgs standard (use: nixfmt file.nix)
      deadnix             # detect unused nix code (use: deadnix file.nix)
      statix              # nix linter / anti-pattern checker (use: statix check .)
      nix-output-monitor  # prettier nix build output (use: nom build ... or pipe: nix build |& nom)
    ];

    # devcell.managedMcp.servers.nixos = {
    #   command = "${bin}/mcp-nixos";
    #   args = [];
    # };
  };
}
