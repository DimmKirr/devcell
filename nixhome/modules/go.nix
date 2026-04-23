# go.nix — Go runtime and toolchain
# Runtime managed by mise; tooling from nixpkgs.
# Not in nixpkgs: terraform-plugin-docs (tfplugindocs) → installed from GitHub release in Dockerfile
{
  pkgs,
  config,
  ...
}: {
  imports = [./mise.nix];

  devcell.mise.tools.go = "1.26.0";

  home.packages = with pkgs; [
    golangci-lint
    gopls
    gotools # goimports, godoc, etc.
    swag    # swagger doc generator (use: swag init)
  ];

  home.sessionVariables = {
    GOPATH = "${config.home.homeDirectory}/go";
    CC = "cc"; # Go defaults to gcc which we don't have; cc → clang via nix
  };

  home.sessionPath = ["${config.home.homeDirectory}/go/bin"];
}
