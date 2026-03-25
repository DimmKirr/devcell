# hosts/macos/home.nix — home-manager config for the vagrant user on the devcell macOS VM
# Reuses the devcell base stack (tmux, jq, ripgrep, go-task, git-lfs, etc.)
{ mcp-nixos, ... }: {
  imports = [
    ../../stacks/base.nix
  ];

  home.username = "vagrant";
  home.homeDirectory = "/Users/vagrant";
  home.stateVersion = "25.11";
}
