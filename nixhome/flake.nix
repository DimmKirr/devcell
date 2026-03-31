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
          builtins.elem (lib.getName pkg) ["claude-code" "corefonts" "drawio" "packer" "terraform"];
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
    # Expose building blocks so user wrapper flakes can compose custom stacks:
    #   devcell.lib.mkHome "x86_64-linux" [ devcell.stacks.go ]
    lib = { inherit mkHome; };
    stacks = lib.mapAttrs'
      (name: mods: lib.nameValuePair (lib.removePrefix "devcell-" name) mods)
      stacks;

    # Individual modules for composing custom stacks in user wrapper flakes:
    #   devcell.lib.mkHome "x86_64-linux" (devcell.stacks.go ++ devcell.modules.electronics)
    modules = {
      apple = [./modules/apple.nix];
      base = [./modules/base.nix];
      build = [./modules/build.nix];
      desktop = [./modules/desktop];
      electronics = [./modules/electronics.nix];
      financial = [./modules/financial.nix];
      go = [./modules/go.nix];
      graphics = [./modules/graphics.nix];
      infra = [./modules/infra.nix];
      llm = [./modules/llm];
      mise = [./modules/mise.nix];
      news = [./modules/news.nix];
      nixos = [./modules/nixos.nix];
      node = [./modules/node.nix];
      project-management = [./modules/project-management.nix];
      python = [./modules/python.nix];
      qa-tools = [./modules/qa-tools.nix];
      scraping = [./modules/scraping];
      shell = [./modules/shell.nix];
      travel = [./modules/travel.nix];
    };

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
