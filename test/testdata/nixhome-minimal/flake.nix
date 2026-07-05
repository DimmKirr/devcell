{
  description = "Minimal nixhome for thin build testing — 1 package, fast switch";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    home-manager = {
      url = "github:nix-community/home-manager/release-25.11";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, home-manager }: let
    lib = nixpkgs.lib;
    user = { username = "devcell"; homeDirectory = "/opt/devcell"; };

    mkConfig = system: modules: home-manager.lib.homeManagerConfiguration {
      pkgs = import nixpkgs { inherit system; };
      modules = [
        {
          home.username = user.username;
          home.homeDirectory = user.homeDirectory;
          home.stateVersion = "25.11";
          programs.home-manager.enable = true;
        }
      ] ++ modules;
    };

    # One config per arch, matching the naming ThinBuildArgv expects.
    # Stack name is `core` — the smallest first-class stack (just
    # home-manager + one package). Used by the cache-roundtrip test to
    # populate a realistic /nix volume in seconds, and by anyone who
    # wants the absolute minimum cell.
    #   devcell-core         (x86_64)
    #   devcell-core-aarch64 (arm64)
    configs = {
      "devcell-core"          = mkConfig "x86_64-linux"  [ ./minimal.nix ];
      "devcell-core-aarch64"  = mkConfig "aarch64-linux" [ ./minimal.nix ];
    };
  in {
    homeConfigurations = configs;
  };
}
