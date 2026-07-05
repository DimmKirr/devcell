# graphics.nix — Graphics tools: Draw.io, Inkscape, GIMP + MCP servers
{
  pkgs,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.modules.graphics;
  bin = config.devcell.managedMcp.nixBinPrefix;

  # inkscape-mcp: Python MCP server exposing Inkscape CLI and DOM operations.
  # Source: https://github.com/grumpydevorg/inkscape-mcps
  inkscape-mcp = pkgs.python3Packages.buildPythonApplication {
    pname = "inkscape-mcp";
    version = "0.1.0";
    src = pkgs.fetchFromGitHub {
      owner = "grumpydevorg";
      repo = "inkscape-mcps";
      rev = "e621da1a8287896fa3a7bf8e3cf4fd6a9c2f87ea";
      hash = "sha256-P+84x+jHg+o0ddsZzwaIHaRZXKzh63gJK9r14x3gFQU=";
    };
    pyproject = true;
    build-system = [pkgs.python3Packages.hatchling];
    nativeBuildInputs = [pkgs.python3Packages.pythonRelaxDepsHook];
    pythonRelaxDeps = ["scour"]; # nixpkgs has 0.38.2; upstream requires <0.38
    dependencies = with pkgs.python3Packages; [
      fastmcp  # MCP framework
      pydantic # data validation
      anyio    # async runtime
      filelock # file locking
      inkex    # Inkscape Python extension library
      scour    # SVG optimizer
    ];
    doCheck = false;
  };

  # gimp-mcp: Python MCP server bridging GIMP 3.2 with AI assistants.
  # 56 tool commands covering every major GIMP operation.
  # Source: https://github.com/maorcc/gimp-mcp (119 stars)
  gimp-mcp = pkgs.python3Packages.buildPythonApplication {
    pname = "gimp-mcp";
    version = "0.1.0";
    src = pkgs.fetchFromGitHub {
      owner = "maorcc";
      repo = "gimp-mcp";
      rev = "09bfb2d3e5ca8efdc50c8d0b8c9cdf590ce422c6";
      hash = "sha256-fmsaDarIQJ9buQjsEYjEet1yCPlQp1w7ZYSYoN//LK0=";
    };
    pyproject = true;
    build-system = [pkgs.python3Packages.setuptools];
    dependencies = with pkgs.python3Packages; [
      mcp      # Model Context Protocol SDK
      fastmcp  # MCP framework
    ];
    doCheck = false;
  };
in {
  options.devcell.modules.graphics = {
    enable = lib.mkEnableOption "Draw.io + Inkscape + GIMP + their MCP servers";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Vector graphics (Inkscape), raster (GIMP), Draw.io headless; MCP for Inkscape + GIMP";
        mcpServers = [ "inkscape-mcp" "gimp-mcp" ];
        sizeMb = 900;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      drawio-headless  # Draw.io headless CLI for .drawio → PNG/SVG/PDF export (use: drawio)
      gimp             # GNU Image Manipulation Program 3.2 (use: gimp)
      gimp-mcp         # GIMP MCP server — 56 AI-driven image editing commands
      inkscape         # vector graphics editor (use: inkscape)
      inkscape-mcp     # Inkscape MCP server for Claude
      potrace          # bitmap → SVG tracer; ships mkbitmap preprocessor (use: potrace, mkbitmap)
    ];

    devcell.managedMcp.servers."inkscape-mcp" = {
      command = "${bin}/inkscape-mcp";
      args = [];
      env = {
        INKS_INKSCAPE_BIN = "${pkgs.inkscape}/bin/inkscape";
        INKS_WORKSPACE = "./";
      };
    };

    devcell.managedMcp.servers."gimp-mcp" = {
      command = "${bin}/gimp-mcp";
      args = [];
      env = {};
    };
  };
}
