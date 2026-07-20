# electronics.nix — KiCad EDA and related tools
# Replaces apt: kicad, libngspice0, ngspice, libspnav0,
#               libocct-{modeling-algorithms,modeling-data,data-exchange,
#                         visualization,foundation,ocaf}-7.8, poppler-utils
#
# kicad pulls in opencascade-occt and wx as transitive dependencies —
# no need to list them explicitly.
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.electronics;
  bin = config.devcell.managedMcp.nixBinPrefix;
  # wokwi-cli: hardware simulator CLI — not in nixpkgs; use pre-built static binary.
  # SHA256 hashes verified from: https://github.com/wokwi/wokwi-cli/releases/tag/v0.26.1
  wokwi-cli = let
    version = "0.26.1";
    sys = pkgs.stdenv.hostPlatform.system;
    asset =
      {
        x86_64-linux = {
          url = "https://github.com/wokwi/wokwi-cli/releases/download/v${version}/wokwi-cli-linuxstatic-x64";
          hash = "sha256-ctloFQurr3UyxHq3dB64WpWmw+PP75H/vP22d/046BE=";
        };
        aarch64-linux = {
          url = "https://github.com/wokwi/wokwi-cli/releases/download/v${version}/wokwi-cli-linuxstatic-arm64";
          hash = "sha256-IF3Az2y5T6xr8/Sxf+nxewwDMk3dAthmwEDKElkBsec=";
        };
        aarch64-darwin = {
          url = "https://github.com/wokwi/wokwi-cli/releases/download/v${version}/wokwi-cli-macos-arm64";
          hash = "sha256-+WUSLcj7o9W/aLdu0BQ8e0zmCRAsLwgkPBEZibJAm8s=";
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
  options.devcell.modules.electronics = {
    enable = lib.mkEnableOption "KiCad EDA + ngspice + ESPHome + PlatformIO + wokwi-cli + kicad-mcp";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "KiCad EDA, SPICE simulation, ESP32/Arduino dev, hardware sim, PCB MCP";
        mcpServers = [ "kicad-mcp" ];
        sizeMb = 800;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs;
      [
        ngspice # SPICE simulation (libngspice0 + ngspice CLI)
        platformio # embedded development platform (Arduino, ESP32, etc.)
        kicadMcp # KiCad MCP server for Claude
        wokwi-cli # Wokwi hardware simulator CLI (Linux + macOS ARM)
        poppler-utils # PDF tools (pdfinfo, pdfimages, etc.)
      ]
      ++ lib.optionals pkgs.stdenv.isLinux [
        esphome # ESP32 framework — bleak (BLE) dep is Linux-only in nixpkgs
        kicad-small # KiCad EDA — OpenGL/mesa, Linux only
        libspnav # 3D mouse / space navigator — Linux input subsystem
      ];

    devcell.managedMcp.servers."kicad-mcp" = {
      command = "${bin}/kicad-mcp";
      args = [];
      # kicad-mcp reads KICAD_PROJECT_PATH from the environment at runtime
    };
  };
}
