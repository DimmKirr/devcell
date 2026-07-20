# hosts/macos/home.nix — home-manager config for the devcell user on the devcell macOS VM
# Reuses the devcell base stack (tmux, jq, ripgrep, go-task, git-lfs, etc.)
{ mcp-nixos, ... }: {
  imports = [
    ../../stacks/base.nix
  ];

  home.username = "devcell";
  home.homeDirectory = "/Users/devcell";
  home.stateVersion = "25.11";
}
