# go.nix — Go runtime and toolchain
# Runtime managed by asdf; tooling from nixpkgs.
# Not in nixpkgs: terraform-plugin-docs (tfplugindocs) → installed from GitHub release in Dockerfile
{
  pkgs,
  config,
  ...
}: {
  programs.asdf = {
    enable = true;
    golang = {
      enable = true;
      defaultVersion = "1.26.0";
    };
    config = {
      legacy_version_file = "yes";
      golang_mod_version_enabled = "yes";
    };
  };

  home.packages = with pkgs; [
    golangci-lint
    gopls
    gotools # goimports, godoc, etc.
  ];

  home.sessionVariables = {
    GOPATH = "${config.home.homeDirectory}/go";
  };

  home.sessionPath = ["${config.home.homeDirectory}/go/bin"];
}
