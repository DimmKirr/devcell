# nixos.nix — Nix/NixOS development tools and MCP server
{
  pkgs,
  mcp-nixos,
  ...
}: let
  mcpPkg = mcp-nixos.packages.${pkgs.system}.default;
in {
  home.packages = with pkgs; [
    mcpPkg
    nix-tree            # visualise nix store dependencies
    nix-diff            # diff nix derivations
    nix-prefetch-github # compute sha256 for fetchFromGitHub (use: nix-prefetch-github owner repo)
    nixfmt-rfc-style    # official nix formatter, nixpkgs standard (use: nixfmt file.nix)
    deadnix             # detect unused nix code (use: deadnix file.nix)
    statix              # nix linter / anti-pattern checker (use: statix check .)
    nix-output-monitor  # prettier nix build output (use: nom build ... or pipe: nix build |& nom)
  ];

  devcell.managedMcp.servers.nixos = {
    command = "${mcpPkg}/bin/mcp-nixos";
    args = [];
  };
}
