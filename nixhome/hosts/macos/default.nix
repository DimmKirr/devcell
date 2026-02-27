# hosts/macos/default.nix — nix-darwin system config for the devcell Vagrant VM
# Applied via: darwin-rebuild switch --flake /nixhome#macOS
{ pkgs, ... }: {
  # Nix daemon settings
  nix.settings = {
    experimental-features = "nix-command flakes";
    allowed-users = [ "vagrant" ];
    trusted-users = [ "root" "vagrant" ];
  };

  # Target Apple Silicon (UTM ARM VMs)
  nixpkgs.hostPlatform = "aarch64-darwin";
  nixpkgs.config.allowUnfree = true;

  # Minimal system packages — user env managed via home-manager
  environment.systemPackages = [ pkgs.git ];

  # Required for nix-darwin
  system.stateVersion = 5;
}
