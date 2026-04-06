# news.nix — RSS/News tools
{pkgs, config, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;
  # inoreader-mcp: Inoreader RSS MCP — 19 tools (feeds, articles, search, tagging, analytics)
  # https://github.com/justmytwospence/inoreader-mcp
  inoreaderMcp = pkgs.buildNpmPackage {
    pname = "inoreader-mcp";
    version = "0.1.0-unstable-2026-03-20";
    src = pkgs.fetchFromGitHub {
      owner = "justmytwospence";
      repo = "inoreader-mcp";
      rev = "0aadb769b632f5d21cfdd4e9ed7cf08b287cda0e";
      hash = "sha256-yx0Ndy/qVIofaYZr3cE19SUz4PKhSUD0Ja+APGHuKA4=";
    };
    npmDepsHash = "sha256-YoaWxGRBnTqytFKZp2p5NW2+hMW5Dh2XiD4D0HamoqQ=";
    nodejs = pkgs.nodejs_22;
  };
in {
  home.packages = [
    inoreaderMcp # Inoreader RSS MCP server (use: inoreader-mcp)
  ];

  # Inoreader — 19 tools: feeds, articles, search, tagging, batch ops, feed health analysis.
  # Requires INOREADER_CLIENT_ID, INOREADER_CLIENT_SECRET env vars at runtime.
  # Get credentials: https://www.inoreader.com/developers/ → create app → redirect URI: http://localhost:3333/callback
  # Auth: use setup_auth tool to complete OAuth flow. Tokens stored in ~/.config/inoreader-mcp/tokens.json
  devcell.managedMcp.servers."inoreader" = {
    command = "${bin}/inoreader-mcp";
    args = [];
    env = {
      INOREADER_CLIENT_ID = "\${INOREADER_CLIENT_ID}";
      INOREADER_CLIENT_SECRET = "\${INOREADER_CLIENT_SECRET}";
    };
  };
}
