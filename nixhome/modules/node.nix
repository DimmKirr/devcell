# node.nix — Node.js runtime
# Runtime managed by mise; npm tools packaged via buildNpmPackage.
{pkgs, ...}: let
  # slidev: presentation slides from Markdown.
  # https://github.com/slidevjs/slidev (pnpm monorepo — requires pnpm_10)
  slidevSrc = pkgs.fetchFromGitHub {
    owner = "slidevjs";
    repo = "slidev";
    rev = "v52.14.1";
    hash = "sha256-GIg4KU2TJMSZXjnB+A8MPZUUp1/M1YX5ctO13dfmOz0=";
  };
  slidev = pkgs.stdenvNoCC.mkDerivation {
    pname = "slidev";
    version = "52.14.1";
    src = slidevSrc;
    pnpmDeps = pkgs.pnpm_10.fetchDeps {
      pname = "slidev";
      version = "52.14.1";
      src = slidevSrc;
      hash = "sha256-UDakhYCqierfXqAbYbcs89mepFngieY88vFcb5Cwo9U=";
      fetcherVersion = 2;
    };
    nativeBuildInputs = [pkgs.pnpm_10.configHook pkgs.nodejs_22 pkgs.makeWrapper];
    buildPhase = "pnpm -r build";
    installPhase = ''
      mkdir -p $out/bin $out/lib
      cp -r . $out/lib/slidev
      makeWrapper ${pkgs.nodejs_22}/bin/node $out/bin/slidev \
        --add-flags $out/lib/slidev/packages/slidev/bin/slidev.mjs
    '';
  };
in {
  imports = [./mise.nix];

  devcell.mise.tools.node = "24.13.1";
  devcell.mise.defaultNpmPackages = ["yarn" "npm"];

  home.packages = [
    pkgs.hugo  # static site generator (use: hugo server)
    slidev     # presentation slides from Markdown (use: slidev)
  ];
}
