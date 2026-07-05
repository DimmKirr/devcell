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

    # nix2container: parallel image build path (spike). Produces content-addressed
    # OCI images alongside images/Dockerfile. See ./packages/image.nix.
    nix2container.url = "github:nlewo/nix2container";
    nix2container.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    self,
    nixpkgs,
    nixpkgs-unstable,
    nixpkgs-edge,
    home-manager,
    nix-darwin,
    mcp-nixos,
    nix2container,
  }: let
    lib = nixpkgs.lib;

    # Fixed nix environment owner. The session user (HOST_USER) is separate and
    # gets nix tools via /opt/devcell dotfiles copied at container startup.
    user = {username = "devcell"; homeDirectory = "/opt/devcell";};

    # Build a homeManagerConfiguration for a given system and list of modules.
    mkHome = system: modules: let
      nixCfg = {
        inherit system;
        # allowUnfree covers: claude-code, corefonts, drawio, packer, terraform,
        # and Android SDK bundles (platform-tools, build-tools, emulator, etc. —
        # too many sub-derivation names to enumerate in a predicate).
        config.allowUnfree = true;
        config.android_sdk.accept_license = true;
      };
      pkgsUnstable = import nixpkgs-unstable nixCfg;
      pkgsEdge = import nixpkgs-edge nixCfg;
    in
      home-manager.lib.homeManagerConfiguration {
        pkgs = import nixpkgs nixCfg;
        extraSpecialArgs = {inherit self mcp-nixos pkgsUnstable pkgsEdge;};
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
      "devcell-core" = [./stacks/core.nix];          # CELL-292: smallest viable (home-manager + 1 pkg)
      "devcell-base" = [./stacks/base.nix];
      "devcell-dev" = [./stacks/dev.nix];           # Modules 2.0 default seed
      "devcell-go" = [./stacks/go.nix];
      "devcell-node" = [./stacks/node.nix];
      "devcell-python" = [./stacks/python.nix];
      "devcell-fullstack" = [./stacks/fullstack.nix];
      "devcell-electronics" = [./stacks/electronics.nix];
      "devcell-ultimate" = [./stacks/ultimate.nix];
    };

    # Modules 2.0 catalog — flat metadata keyed by module name (CELL-65).
    # CLI reads this with: nix eval .#devcellModules --json
    # Source of truth: each module's `options.devcell.modules.<name>.meta`
    # attribute; this attrset mirrors them statically so CLI can read without
    # evaluating the full home-manager module system.
    devcellModules = {
      android        = { description = "Android dev: ADB+fastboot (all arch), Android SDK + emulator + apktool + jadx (x86_64 only)"; mcpServers = []; sizeMb = 2500; };
      apple          = { description = "Swift toolchain for CGO and Apple-platform cross-compilation"; mcpServers = []; sizeMb = 900; };
      build          = { description = "C/C++ build toolchain: clang/cmake/make/llvm/lld"; mcpServers = []; sizeMb = 1500; };
      desktop        = { description = "GUI desktop: Fluxbox WM, Xvfb display, VNC + RDP servers, PulseAudio, screenshot tools"; mcpServers = []; sizeMb = 1200; };
      electronics    = { description = "KiCad EDA, SPICE simulation, ESP32/Arduino dev, hardware sim, PCB MCP"; mcpServers = ["kicad-mcp"]; sizeMb = 800; };
      financial      = { description = "Yahoo Finance, SEC EDGAR, FRED — market data, filings, economic time series"; mcpServers = ["yahoo-finance" "edgartools" "mcp-fredapi"]; sizeMb = 500; };
      go             = { description = "Go toolchain: mise-managed runtime + golangci-lint, gopls, gotools"; mcpServers = []; sizeMb = 350; };
      graphics       = { description = "Vector graphics (Inkscape), raster (GIMP), Draw.io headless; MCP for Inkscape + GIMP"; mcpServers = ["inkscape-mcp" "gimp-mcp"]; sizeMb = 900; };
      infra          = { description = "IaC + Cloud: Terraform/OpenTofu, AWS CLI v2, Helm, Packer, Porter, MCPs for AWS API/CloudWatch/OpenTofu/Notion"; mcpServers = ["aws-api" "cloudwatch" "opentofu" "notion-api"]; sizeMb = 1200; };
      news           = { description = "Inoreader RSS — feeds, articles, search, tagging"; mcpServers = ["inoreader"]; sizeMb = 50; };
      nixos          = { description = "Nix dev tooling: nix-tree, nix-diff, nixfmt, deadnix, statix, nom"; mcpServers = []; sizeMb = 30; };
      node           = { description = "Node.js runtime (mise) + Hugo static site generator"; mcpServers = []; sizeMb = 200; };
      plex           = { description = "Plex Media Server control via MCP — needs PLEX_URL + PLEX_TOKEN"; mcpServers = ["plex"]; sizeMb = 80; };
      postgresql     = { description = "Local PostgreSQL 17 — auto-started in entrypoint, default db created"; mcpServers = []; sizeMb = 60; };
      project-management = { description = "Hubstaff time tracking, n8n workflows, Linear (HTTP), Atlassian Jira/Confluence (HTTP)"; mcpServers = ["hubstaff-mcp" "n8n" "linear-server" "atlassian"]; sizeMb = 250; };
      publishing     = { description = "Document publishing: LaTeX + Pandoc + Typst + Marp slides + Biber bibliography"; mcpServers = []; sizeMb = 1700; };
      python         = { description = "Python runtime (mise) + uv fast package manager"; mcpServers = []; sizeMb = 250; };
      qa-tools       = { description = "MailSlurp — create inboxes, read/list/clear emails programmatically for QA"; mcpServers = ["mailslurp"]; sizeMb = 60; };
      scraping       = { description = "Patchright stealth browser MCP — anti-bot Chromium for Cloudflare/Kasada-grade sites"; mcpServers = ["playwright"]; sizeMb = 700; };
      security       = { description = "Vuln scanners + fuzzers + recon + RE + forensics (nuclei, nmap, sqlmap, ghidra, ...)"; mcpServers = []; sizeMb = 3500; };
      travel         = { description = "Google Maps (geocoding, routing, places) + TripIt (trips, itineraries)"; mcpServers = ["google-maps" "tripit"]; sizeMb = 100; };
    };

    # Modules 2.0 profiles — named compositions (CELL-63).
    # CLI: nix eval .#devcellProfiles --json
    devcellProfiles = {
      base = [];
      dev = ["scraping" "infra"];
      ultimate = [
        # from fullstack
        "build" "go" "apple" "infra" "node" "project-management" "python" "qa-tools" "scraping"
        # ultimate additions
        "android" "desktop" "electronics" "graphics" "news" "nixos"
        "postgresql" "publishing" "security" "travel" "plex"
      ];
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

    # Vagrant VM configs — same stacks but for the 'vagrant' user at /home/vagrant.
    # Applied by the Vagrantfile provisioner:
    #   home-manager switch --flake .#vagrant-ultimate-aarch64
    vagrantUser = {username = "vagrant"; homeDirectory = "/home/vagrant";};
    mkVagrantHome = system: modules: let
      nixCfg = {
        inherit system;
        config.allowUnfree = true;
        config.android_sdk.accept_license = true;
      };
      pkgsUnstable = import nixpkgs-unstable nixCfg;
      pkgsEdge = import nixpkgs-edge nixCfg;
    in
      home-manager.lib.homeManagerConfiguration {
        pkgs = import nixpkgs nixCfg;
        extraSpecialArgs = {inherit self mcp-nixos pkgsUnstable pkgsEdge;};
        modules =
          [
            {
              home.stateVersion = "25.11";
              home.username = vagrantUser.username;
              home.homeDirectory = vagrantUser.homeDirectory;
            }
          ]
          ++ modules;
      };
    mkAllVagrantConfigs =
      lib.foldlAttrs
      (
        acc: name: mods: let
          shortName = lib.removePrefix "devcell-" name;
        in
          acc
          // {"vagrant-${shortName}" = mkVagrantHome "x86_64-linux" mods;}
          // {"vagrant-${shortName}-aarch64" = mkVagrantHome "aarch64-linux" mods;}
      )
      {}
      stacks;
  in {
    # Modules 2.0 catalog + profiles (CELL-65, CELL-63).
    # CLI reads these with:
    #   nix eval .#devcellModules --json   # full catalog with metadata
    #   nix eval .#devcellProfiles --json  # named compositions
    inherit devcellModules devcellProfiles;

    # Expose building blocks so user wrapper flakes can compose custom stacks:
    #   devcell.lib.mkHome "x86_64-linux" [ devcell.stacks.go ]
    lib = { inherit mkHome; };
    stacks = lib.mapAttrs'
      (name: mods: lib.nameValuePair (lib.removePrefix "devcell-" name) mods)
      stacks;

    # Individual modules for composing custom stacks in user wrapper flakes:
    #   devcell.lib.mkHome "x86_64-linux" (devcell.stacks.go ++ devcell.modules.electronics)
    modules = {
      android = [./modules/android.nix];
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
      media = [./modules/media];                     # bundles plex
      mise = [./modules/mise.nix];
      news = [./modules/news.nix];
      nixos = [./modules/nixos.nix];
      node = [./modules/node.nix];
      plex = [./modules/media/plex.nix];
      postgresql = [./modules/postgresql.nix];
      project-management = [./modules/project-management.nix];
      python = [./modules/python.nix];
      qa-tools = [./modules/qa-tools.nix];
      scraping = [./modules/scraping];
      security = [./modules/security.nix];
      shell = [./modules/shell.nix];
      travel = [./modules/travel.nix];
    };

    homeConfigurations = mkAllConfigs // mkAllVagrantConfigs;

    # ── nix2container parallel image build (spike) ─────────────────────────────
    # Outputs (keyed on HOST system, not image target):
    #   packages.<host-system>.devcell-<stack>-pure-image
    #     → builds an OCI image via nix2container. Image content targets Linux
    #       (Docker Desktop's container VM kernel) regardless of host arch.
    #       Tag: devcell-user:<stack>-pure.
    #   packages.<host-system>.skopeo-nix2container
    #     → host-arch patched skopeo with the `nix:` transport, used by `cell`
    #       to load the built image into the local Docker daemon.
    #
    # Host-keying matters for two reasons:
    #   1. The loader skopeo must run on the host (Mac kernels can't exec
    #      Linux ELF, and vice versa).
    #   2. nix2container's IFD helpers (closure-graph/layers/copy-to-docker-daemon
    #      bash scripts) are ALSO host-arch — `nix build` runs them locally
    #      during eval. If they were target-arch (aarch64-linux), `cell --pure`
    #      on a Mac would fail with "Exec format error" at the IFD step.
    # See mkImagePackagesFor below for how host/target are decoupled.
    packages = let
      # nix2container's loader-side skopeo, built for `hostSystem`. Used by
      # cell on the host to invoke `skopeo copy nix:... docker-daemon:...`.
      # The patch (which adds the `nix:` transport) requires skopeo ≥1.21
      # (containers/skopeo PR #2688: `vendor/go.podman.io/image/v5/` layout).
      # Main nixpkgs at the current pin ships skopeo 1.20.0, so we override
      # ONLY skopeo with the unstable variant — nix2container-bin (the Go
      # binary) stays cached on main nixpkgs and avoids the prior 0-byte-
      # closure-graph.json regression from a wholesale pkgs swap.
      mkN2c = hostSystem: let
        pkgsHost = import nixpkgs {
          system = hostSystem;
          config.allowUnfree = true;
        };
        pkgsHostUnstable = import nixpkgs-unstable {
          system = hostSystem;
          config.allowUnfree = true;
        };
        # nix2container's default.nix builds its Go binary via
        # `pkgs.buildGoModule`. That build invokes `go mod download` during
        # the `goModules` (vendor) phase, and Go ≥1.21's telemetry init
        # writes $HOME/.config/go/telemetry/... whenever $HOME is writable.
        # Under nix `sandbox = false` (which we require because Docker
        # containers lack the user namespaces nix's sandbox needs), $HOME
        # defaults to /homeless-shelter and IS writable. The leftover dir
        # then trips nix's pre-build purity check on every subsequent pure
        # build:
        #
        #   error: home directory '/homeless-shelter' exists; please remove
        #   it to assure purity of builds without sandboxing
        #
        # Idiomatic fix per NixOS Discourse #7540 + nixpkgs#208036: redirect
        # $HOME to a fresh tmpdir via `preBuild`. Because nix2container
        # exports `nix2container` as a helper attrset (not a derivation),
        # we can't `overrideAttrs` it directly — instead we wrap
        # `pkgs.buildGoModule` upstream of the import, so nix2container's
        # own derivation picks up the HOME redirect when its binary is
        # built. `preBuild` propagates to the `goModules` vendor derivation.
        # Refs:
        #   https://github.com/NixOS/nix/issues/11295
        #   https://discourse.nixos.org/t/.../7540
        #   https://github.com/NixOS/nixpkgs/issues/208036
        buildGoModuleWithHome = args: pkgsHost.buildGoModule (args // {
          preBuild = (args.preBuild or "") + ''
            export HOME=$(mktemp -d)
          '';
        });
        pkgsForN2c = pkgsHost // {
          skopeo = pkgsHostUnstable.skopeo;
          buildGoModule = buildGoModuleWithHome;
        };
      in (import nix2container { pkgs = pkgsForN2c; });

      # Image packages, parametric in (hostSystem, targetSystem). The image
      # CONTENT is always Linux — Docker Desktop's container VM runs the
      # Linux kernel regardless of host arch. The nix2container HELPERS
      # (copy-to-docker-daemon, IFD layer/closure-graph bash scripts, the
      # bundled jq+skopeo wrapper) must run on the HOST — `nix build` realizes
      # them locally during the eval/IFD phase, and a Mac kernel cannot exec
      # an aarch64-linux ELF. Decoupling host from target via this function
      # is what makes `cell --pure` work on a Mac with a linux-builder: the
      # remote builder produces the Linux image content, the host produces
      # the helpers that wire it all together.
      mkImagePackagesFor = hostSystem: targetSystem: let
        pkgsForSys = import nixpkgs {
          system = targetSystem;
          config.allowUnfree = true;
          config.android_sdk.accept_license = true;
        };
        pkgsUnstableForSys = import nixpkgs-unstable {
          system = targetSystem;
          config.allowUnfree = true;
        };
        # n2c lib instantiated against HOST pkgs — its helpers (bash, jq,
        # the writeShellApplication wrappers) inherit the host's arch. Image
        # content still comes from pkgsForSys below, so the resulting OCI
        # image targets `targetSystem` even when host ≠ target.
        n2c = (mkN2c hostSystem).nix2container;
        mkStackImage = stackName: let
          archSuffix =
            if targetSystem == "aarch64-linux"
            then "-aarch64"
            else "";
          cfgKey = "devcell-${stackName}${archSuffix}";
        in
          import ./packages/image.nix {
            pkgs = pkgsForSys;
            pkgsUnstable = pkgsUnstableForSys;
            pkgsEdge = import nixpkgs-edge {
              system = targetSystem;
              config.allowUnfree = true;
              config.android_sdk.accept_license = true;
            };
            nix2container = n2c;
            homeConfig = mkAllConfigs.${cfgKey};
            stackName = stackName;
            tag = "${stackName}-pure";
          };
      in {
        "devcell-base-pure-image" = mkStackImage "base";
        "devcell-python-pure-image" = mkStackImage "python";
        "devcell-fullstack-pure-image" = mkStackImage "fullstack";
        "devcell-ultimate-pure-image" = mkStackImage "ultimate";
        # Loader skopeo for the host arch — used by `cell` on the host to
        # `skopeo copy nix:... docker-daemon:...` after the image build.
        "skopeo-nix2container" = (mkN2c hostSystem).skopeo-nix2container;
      };

      # Pick the natural Linux target for a given host arch. Both Darwin
      # arches build the matching Linux arch by default; cross-arch (e.g.
      # x86 Mac → aarch64-linux image) isn't exposed here — set up a
      # separate flake output if you need it.
      linuxTargetFor = hostSystem:
        if hostSystem == "x86_64-darwin" || hostSystem == "x86_64-linux"
        then "x86_64-linux"
        else "aarch64-linux";
    in {
      "x86_64-linux"   = mkImagePackagesFor "x86_64-linux"   (linuxTargetFor "x86_64-linux");
      "aarch64-linux"  = mkImagePackagesFor "aarch64-linux"  (linuxTargetFor "aarch64-linux");
      "x86_64-darwin"  = mkImagePackagesFor "x86_64-darwin"  (linuxTargetFor "x86_64-darwin");
      "aarch64-darwin" = mkImagePackagesFor "aarch64-darwin" (linuxTargetFor "aarch64-darwin");
    };

    # macOS VM (Vagrant/UTM) — applied via: darwin-rebuild switch --flake .#macOS
    darwinConfigurations.macOS = nix-darwin.lib.darwinSystem {
      system = "aarch64-darwin";
      modules = [
        ./hosts/macos/default.nix
        home-manager.darwinModules.home-manager
        {
          # Pass flake inputs into home-manager modules (needed by base.nix → managed-*.nix)
          home-manager.extraSpecialArgs = {
            inherit self mcp-nixos;
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
