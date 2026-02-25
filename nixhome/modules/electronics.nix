# electronics.nix — KiCad EDA and related tools
# Replaces apt: kicad, libngspice0, ngspice, libspnav0,
#               libocct-{modeling-algorithms,modeling-data,data-exchange,
#                         visualization,foundation,ocaf}-7.8, poppler-utils
#
# kicad pulls in opencascade-occt and wx as transitive dependencies —
# no need to list them explicitly.
{pkgs, ...}: let
  # wokwi-cli: hardware simulator CLI — not in nixpkgs; use pre-built static binary.
  # SHA256 hashes verified from: https://github.com/wokwi/wokwi-cli/releases/tag/v0.26.0
  wokwi-cli = let
    version = "0.26.0";
    sys = pkgs.stdenv.hostPlatform.system;
    asset =
      {
        x86_64-linux = {
          url = "https://github.com/wokwi/wokwi-cli/releases/download/v${version}/wokwi-cli-linuxstatic-x64";
          hash = "sha256-uRBti3m40GrblnP0eylEQfkzGV1l4LllqtVhTvr6WHY=";
        };
        aarch64-linux = {
          url = "https://github.com/wokwi/wokwi-cli/releases/download/v${version}/wokwi-cli-linuxstatic-arm64";
          hash = "sha256-dW7ZiIRyrqiXKv9IOG70z1FOK9Fo9i8D5y7+BWCZ2U4=";
        };
      }.${
        sys
      } or (throw "wokwi-cli: unsupported platform ${sys}");
  in
    pkgs.stdenvNoCC.mkDerivation {
      pname = "wokwi-cli";
      inherit version;
      src = pkgs.fetchurl {inherit (asset) url hash;};
      dontUnpack = true;
      installPhase = ''
        install -Dm755 $src $out/bin/wokwi-cli
      '';
    };
  # kicad-mcp: Python MCP server exposing KiCad EDA to Claude.
  # All deps (mcp, fastmcp, pandas, pyyaml, defusedxml) are in nixpkgs 25.11.
  kicadMcp = pkgs.python3Packages.buildPythonApplication {
    pname = "kicad-mcp";
    version = "0-unstable-2025-02-24";
    src = pkgs.fetchFromGitHub {
      owner = "lamaalrajih";
      repo = "kicad-mcp";
      rev = "98c9ea41cb393393a8bafd157a93e84431e00afb";
      hash = "sha256-45+uc0QMqQKCRkmUOq/+F36Ap4Ab3iiJy0kTqDz2SeI=";
    };
    pyproject = true;
    build-system = [pkgs.python3Packages.hatchling];
    dependencies = with pkgs.python3Packages; [
      mcp
      fastmcp
      pandas
      pyyaml
      defusedxml
    ];
    doCheck = false;
  };
in {
  home.packages = with pkgs;
    [
      kicad-small # KiCad EDA without 3D models (saves ~6 GB)
      ngspice # SPICE simulation (libngspice0 + ngspice CLI)
      libspnav # 3D mouse / space navigator support
      esphome # ESP32 framework for home automation
      wokwi-cli # Wokwi hardware simulator CLI (v0.26.0 static binary)
      kicadMcp # KiCad MCP server for Claude
    ]
    ++ [
      pkgs."poppler-utils" # PDF tools (pdfinfo, pdfimages, etc.)
    ];

  devcell.managedMcp.servers."kicad-mcp" = {
    command = "kicad-mcp";
    args = [];
    # kicad-mcp reads KICAD_PROJECT_PATH from the environment at runtime
  };
}
