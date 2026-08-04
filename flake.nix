{
  description = "devcell — container-native dev environments";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  };

  outputs = {
    self,
    nixpkgs,
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin"];
    forAllSystems = f:
      nixpkgs.lib.genAttrs systems (system:
        f {
          inherit system;
          pkgs = import nixpkgs {inherit system;};
        });

    # Tight source filter — only the Go files needed to compile cell.
    # Excludes test/results/, web/, nixhome/, docs/, etc. so each
    # nix-build doesn't copy the entire repo into the store.
    cellSrc = nixpkgs.lib.fileset.toSource {
      root = ./.;
      fileset = nixpkgs.lib.fileset.unions [
        ./go.mod
        ./go.sum
        ./cmd
        ./internal
      ];
    };

    # Version stamped into the binary via -ldflags. Consumers pinning a
    # release (`nix profile install github:DimmKirr/devcell/v0.8.2#cell`)
    # override this by pointing `nix build` at a tagged ref and passing
    # `--override-input` or by editing this string in a release commit.
    # `self.shortRev` is populated when Nix evaluates a clean flake ref
    # (git tag / commit); falls back to "dirty" for uncommitted trees.
    cellVersion = "v0.0.0-dev";
    cellCommit = self.shortRev or self.dirtyShortRev or "unknown";
    cellBuildDate = self.lastModifiedDate or "unknown";
    versionPkg = "github.com/DimmKirr/devcell/internal/version";
  in {
    packages = forAllSystems ({
      system,
      pkgs,
    }: rec {
      # cell — devcell CLI (Go binary). buildGo126Module reads
      # ${src}/go.mod, fetches the module closure via Go's tooling
      # inside the nix sandbox, and pins the closure under vendorHash.
      # Update vendorHash whenever go.sum changes — nix-build will
      # error with the new hash to substitute.
      cell = pkgs.buildGo126Module {
        pname = "cell";
        version = nixpkgs.lib.removePrefix "v" cellVersion;
        src = cellSrc;

        vendorHash = "sha256-1Rfwj9lUFixFpprDztbRqxEf6KCpgJitUAbW2YOssFE=";

        subPackages = ["cmd"];

        # Stamp version.Version / GitCommit / BuildDate into the binary
        # so `cell --version` reports the flake's tag/commit instead of
        # the hardcoded "v0.0.0" defaults in internal/version/version.go.
        # Mirrors Taskfile.yml CELL_LDFLAGS and .goreleaser.yaml.
        ldflags = [
          "-s"
          "-w"
          "-X ${versionPkg}.Version=${cellVersion}"
          "-X ${versionPkg}.GitCommit=${cellCommit}"
          "-X ${versionPkg}.BuildDate=${cellBuildDate}"
        ];

        # Tests need Docker / GHCR auth / real /nix volumes — none of
        # which the nix sandbox provides. The full suite runs via
        # `task test:unit` + `task test:integration` in CI.
        doCheck = false;

        # Swagger docs are generated at build time by the Dockerfile
        # path; the nix derivation doesn't need them. Stub the docs
        # package so cmd/serve.go's import compiles.
        preBuild = ''
          mkdir -p docs
          cat > docs/docs.go << 'EOF'
          package docs
          EOF
        '';

        # Rename the binary from "cmd" to "cell".
        postInstall = ''
          mv $out/bin/cmd $out/bin/cell
        '';

        meta = with pkgs.lib; {
          description = "devcell CLI — container-native dev environments";
          homepage = "https://github.com/DimmKirr/devcell";
          license = licenses.mit;
          mainProgram = "cell";
        };
      };
      default = cell;
    });

    # Home-manager module: configure the GLOBAL devcell config declaratively.
    #   imports = [ devcell.homeManagerModules.default ];
    #   devcell = { enable = true; prompt = "..."; op.documents = [ ... ]; };
    # Renders ~/.config/devcell/devcell.toml and installs the cell CLI from
    # this flake (override with devcell.package). Option tree is generated
    # from internal/cfg.CellConfig by `task hm:generate` — see
    # nix/home-manager/options.nix.
    homeManagerModules = rec {
      devcell = { config, lib, pkgs, ... }: {
        imports = [ ./nix/home-manager/module.nix ];
        config.devcell.package =
          lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.cell;
      };
      default = devcell;
    };

    # `nix develop` for local hacking — provides Go 1.26 + tooling.
    # nix-update rewrites `vendorHash` in this file after go.mod/go.sum
    # changes; pre-commit dispatches the hook defined in
    # .pre-commit-config.yaml. Both are pulled in here so contributors
    # get them for free by entering the dev shell.
    devShells = forAllSystems ({
      system,
      pkgs,
    }: {
      default = pkgs.mkShellNoCC {
        packages = [
          pkgs.go_1_26
          pkgs.go-task
          pkgs.nix-update
          pkgs.pre-commit
        ];
        shellHook = ''
          if [ -d .git ] && [ -f .pre-commit-config.yaml ] && \
             [ ! -f .git/hooks/pre-commit ]; then
            pre-commit install --install-hooks >/dev/null
          fi
        '';
      };
    });
  };
}
