# go.nix — Go runtime and toolchain
# Runtime managed by mise; tooling from nixpkgs.
# Not in nixpkgs: terraform-plugin-docs (tfplugindocs) → installed from GitHub release in Dockerfile
{
  pkgs,
  config,
  lib,
  ...
}: let
  cfg = config.devcell.modules.go;
in {
  # imports must stay top-level
  imports = [./mise.nix];

  options.devcell.modules.go = {
    enable = lib.mkEnableOption "Go runtime + tooling (golangci-lint, gopls, gotools, go-swag)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Go toolchain: mise-managed runtime + golangci-lint, gopls, gotools";
        mcpServers = [ ];
        sizeMb = 350;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    devcell.mise.tools.go = "1.26.0";

    home.packages = with pkgs; [
      golangci-lint
      gopls
      gotools # goimports, godoc, etc.
      go-swag # swagger doc generator (use: swag init)
    ];

    home.sessionVariables = {
      GOPATH = "${config.home.homeDirectory}/go";
      CC = "cc"; # Go defaults to gcc which we don't have; cc → clang via nix
    };

    home.sessionPath = ["${config.home.homeDirectory}/go/bin"];
  };
}
