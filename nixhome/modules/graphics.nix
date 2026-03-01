# graphics.nix — Inkscape vector graphics editor + MCP server
{
  pkgs,
  lib,
  ...
}: let
  # inkscape-mcp: Python MCP server exposing Inkscape CLI and DOM operations.
  # Source: https://github.com/grumpydevorg/inkscape-mcps
  # TODO: pin to specific commit; run: nix-prefetch-github grumpydevorg inkscape-mcps
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
in {
  home.packages = with pkgs; [
    inkscape     # vector graphics editor (use: inkscape)
    inkscape-mcp # Inkscape MCP server for Claude
  ];

  devcell.managedMcp.servers."inkscape-mcp" = {
    command = "inkscape-mcp";
    args = [];
    env = {
      INKS_INKSCAPE_BIN = "${pkgs.inkscape}/bin/inkscape";
    };
  };
}
