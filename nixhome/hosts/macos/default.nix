# hosts/macos/default.nix — nix-darwin system config for the devcell macOS VM
# Applied via: nix run nix-darwin -- switch --flake /Volumes/nixhome#<stack>
{ pkgs, ... }: {
  # Nix daemon settings
  nix.settings = {
    experimental-features = "nix-command flakes";
    allowed-users = [ "devcell" ];
    trusted-users = [ "root" "devcell" ];
  };

  # Target Apple Silicon (tart ARM VMs)
  nixpkgs.hostPlatform = "aarch64-darwin";
  nixpkgs.config.allowUnfree = true;

  # Declare the devcell user so home-manager's common.nix can resolve
  # homeDirectory from users.users.devcell.home (otherwise it's null).
  users.users.devcell = {
    uid = 502;
    home = "/Users/devcell";
    shell = pkgs.bashInteractive;
  };
  users.knownUsers = [ "devcell" ];

  # nix-darwin creates the user via dscl but doesn't mkdir the home directory.
  # home-manager's activate script does `cd $HOME` early, so ensure it exists
  # before activation proceeds. preActivation runs before users/groups/etc.
  # Also grants devcell passwordless sudo (home-manager activation scripts need
  # it for installing LaunchDaemons and managed tools).
  system.activationScripts.preActivation.text = ''
    if [ ! -d /Users/devcell ]; then
      mkdir -p /Users/devcell
      chown 502:staff /Users/devcell
      echo "created /Users/devcell (uid 502)"
    fi
    if [ ! -f /etc/sudoers.d/devcell ]; then
      mkdir -p /etc/sudoers.d
      echo "devcell ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/devcell
      chmod 0440 /etc/sudoers.d/devcell
      echo "granted devcell passwordless sudo"
    fi
  '';

  # Minimal system packages — user env managed via home-manager
  environment.systemPackages = [ pkgs.git ];

  # Required for nix-darwin
  system.stateVersion = 5;
}
