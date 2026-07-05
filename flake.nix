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
        version = "0.0.0";
        src = cellSrc;

        vendorHash = "sha256-0jKFCqnPD4pkekFO3pJ9q6NqoDH2KFt6Zg+cc4wM+mc=";

        subPackages = ["cmd"];

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

    # `nix develop` for local hacking — provides Go 1.26 + tooling.
    devShells = forAllSystems ({
      system,
      pkgs,
    }: {
      default = pkgs.mkShellNoCC {
        packages = [pkgs.go_1_26 pkgs.go-task];
      };
    });
  };
}
