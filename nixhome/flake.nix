{
  description = "devcell container tool profiles";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    home-manager = {
      url = "github:nix-community/home-manager/release-25.11";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-darwin = {
      url = "github:LnL7/nix-darwin/nix-darwin-25.11";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    asdf = {
      url = "github:DimmKirr/nix-asdf";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    mcp-nixos.url = "github:utensils/mcp-nixos";
  };

  outputs = {
    self,
    nixpkgs,
    home-manager,
    nix-darwin,
    asdf,
    mcp-nixos,
  }: let
    lib = nixpkgs.lib;

    # Fixed nix environment owner. The session user (HOST_USER) is separate and
    # gets nix tools via /opt/devcell dotfiles copied at container startup.
    user = {username = "devcell"; homeDirectory = "/opt/devcell";};

    # Build a homeManagerConfiguration for a given system and list of modules.
    mkHome = system: modules:
      home-manager.lib.homeManagerConfiguration {
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfreePredicate = pkg:
            builtins.elem (lib.getName pkg) ["packer" "terraform"];
        };
        extraSpecialArgs = {inherit mcp-nixos;};
        modules =
          [
            asdf.homeManagerModules.default
            {
              home.stateVersion = "25.11";
              home.username = user.username;
              home.homeDirectory = user.homeDirectory;
            }
          ]
          ++ modules;
      };

    # Map of profile name → module list
    profiles = {
      "devcell-base" = [./profiles/base.nix];
      "devcell-go" = [./profiles/go.nix];
      "devcell-node" = [./profiles/node.nix];
      "devcell-python" = [./profiles/python.nix];
      "devcell-fullstack" = [./profiles/fullstack.nix];
      "devcell-electronics" = [./profiles/electronics.nix];
      "devcell-ultimate" = [./profiles/ultimate.nix];
    };

    # Generate homeConfigurations for x86_64-linux and aarch64-linux.
    # aarch64 variants use a "-aarch64" suffix so the Dockerfile can select
    # the right config via TARGETARCH:
    #   ARCH_SUFFIX=$([ "$TARGETARCH" = "arm64" ] && echo "-aarch64" || echo "")
    #   home-manager switch --flake .#devcell-fullstack${ARCH_SUFFIX}
    mkAllConfigs =
      lib.foldlAttrs
      (
        acc: name: mods:
          acc
          // {"${name}" = mkHome "x86_64-linux" mods;}
          // {"${name}-aarch64" = mkHome "aarch64-linux" mods;}
      )
      {}
      profiles;
  in {
    homeConfigurations = mkAllConfigs;

    # macOS VM (Vagrant/UTM) — applied via: darwin-rebuild switch --flake .#macOS
    darwinConfigurations.macOS = nix-darwin.lib.darwinSystem {
      system = "aarch64-darwin";
      modules = [
        ./hosts/macos/default.nix
        home-manager.darwinModules.home-manager
        {
          # Pass flake inputs into home-manager modules (needed by base.nix → managed-*.nix)
          home-manager.extraSpecialArgs = {inherit asdf mcp-nixos;};
          home-manager.useGlobalPkgs = true;
          home-manager.useUserPackages = true;
          home-manager.users.vagrant = import ./hosts/macos/home.nix;
        }
      ];
    };
  };
}
