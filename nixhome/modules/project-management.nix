# project-management.nix — Project management and time-tracking MCP servers
{pkgs, config, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;
  # hubstaff-mcp: Python MCP server for Hubstaff time tracking and project management.
  # https://github.com/cdmx-in/hubstaff-mcp
  # All deps (mcp, httpx, pydantic, python-dotenv) are in nixpkgs 25.11.
  hubstaffMcp = pkgs.python3Packages.buildPythonApplication {
    pname = "hubstaff-mcp";
    version = "0.1.3-unstable-2026-03-27";
    src = pkgs.fetchFromGitHub {
      owner = "cdmx-in";
      repo = "hubstaff-mcp";
      rev = "c6cf0860951c196e94ea829808cc56f98f79deb2";
      hash = "sha256-zV1/SGezx2ZynK+YnhCiQWIqPQFxtVyy8jiWZx/PULA=";
    };
    pyproject = true;
    build-system = [pkgs.python3Packages.hatchling];
    dependencies = with pkgs.python3Packages; [
      mcp
      httpx
      pydantic
      python-dotenv
    ];
    doCheck = false;
  };
in {
  home.packages = [
    hubstaffMcp  # Hubstaff MCP server for time tracking (use: hubstaff-mcp)
  ];

  devcell.managedMcp.servers."hubstaff-mcp" = {
    command = "${bin}/hubstaff-mcp";
    args = [];
    # Requires HUBSTAFF_REFRESH_TOKEN env var at runtime (personal access token)
  };

  # Linear — remote HTTP MCP server.
  # Auth: OAuth 2.1 flow on first use (run /mcp in Claude session to authenticate).
  devcell.managedMcp.servers."linear-server" = {
    type = "http";
    url = "https://mcp.linear.app/mcp";
  };
}
