{
  description = "devcell container tool stacks";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    nixpkgs-edge.url = "github:NixOS/nixpkgs/master";
    home-manager = {
      url = "github:nix-community/home-manager/release-25.11";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-darwin = {
      url = "github:LnL7/nix-darwin/nix-darwin-25.11";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    mcp-nixos.url = "github:utensils/mcp-nixos";
  };

  outputs = {
    self,
    nixpkgs,
    nixpkgs-unstable,
    nixpkgs-edge,
    home-manager,
    nix-darwin,
    mcp-nixos,
  }: let
    lib = nixpkgs.lib;

    # Fixed nix environment owner. The session user (HOST_USER) is separate and
    # gets nix tools via /opt/devcell dotfiles copied at container startup.
    user = {username = "devcell"; homeDirectory = "/opt/devcell";};

    # Build a homeManagerConfiguration for a given system and list of modules.
    mkHome = system: modules: let
      nixCfg = {
        inherit system;
        config.allowUnfreePredicate = pkg:
          builtins.elem (lib.getName pkg) ["claude-code" "corefonts" "packer" "terraform"];
      };
      pkgsUnstable = import nixpkgs-unstable nixCfg;
      pkgsEdge = import nixpkgs-edge nixCfg;
    in
      home-manager.lib.homeManagerConfiguration {
        pkgs = import nixpkgs nixCfg;
        extraSpecialArgs = {inherit mcp-nixos pkgsUnstable pkgsEdge;};
        modules =
          [
            {
              home.stateVersion = "25.11";
              home.username = user.username;
              home.homeDirectory = user.homeDirectory;
            }
          ]
          ++ modules;
      };

    # Map of stack name → module list
    stacks = {
      "devcell-base" = [./stacks/base.nix];
      "devcell-go" = [./stacks/go.nix];
      "devcell-node" = [./stacks/node.nix];
      "devcell-python" = [./stacks/python.nix];
      "devcell-fullstack" = [./stacks/fullstack.nix];
      "devcell-electronics" = [./stacks/electronics.nix];
      "devcell-ultimate" = [./stacks/ultimate.nix];
    };

    # Generate homeConfigurations for x86_64-linux and aarch64-linux.
    # aarch64 stacks use a "-aarch64" suffix so the Dockerfile can select
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
      stacks;
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
          home-manager.extraSpecialArgs = {
            inherit mcp-nixos;
            pkgsUnstable = import nixpkgs-unstable {
              system = "aarch64-darwin";
              config.allowUnfree = true;
            };
            pkgsEdge = import nixpkgs-edge {
              system = "aarch64-darwin";
              config.allowUnfree = true;
            };
          };
          home-manager.useGlobalPkgs = true;
          home-manager.useUserPackages = true;
          home-manager.users.vagrant = import ./hosts/macos/home.nix;
        }
      ];
    };
  };
}
