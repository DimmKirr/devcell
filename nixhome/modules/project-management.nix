# project-management.nix — Project management, time-tracking, and workflow-automation MCP servers
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

  # n8n-mcp: Node MCP server bridging Claude/agents to an n8n workflow-automation instance.
  # https://github.com/czlonkowski/n8n-mcp
  n8nMcp = pkgs.buildNpmPackage {
    pname = "n8n-mcp";
    version = "2.47.14";
    src = pkgs.fetchFromGitHub {
      owner = "czlonkowski";
      repo = "n8n-mcp";
      rev = "v2.47.14";
      hash = "sha256-nHuWh3hMkvXnUZQcex5pmxF627UlZwVP01ekTI7QCdI=";
    };
    npmDepsHash = "sha256-x/gzRVq7rhnNGNGzG3UU/V4SSwCD0FXspvtx5gLf5iE=";
    # --legacy-peer-deps: upstream's lockfile has unresolvable peer-dep conflicts
    # (langchain/langgraph vs langchain/core, huggingface/inference vs langchain/community).
    # Without it, npm's FOD prefetch silently skips conflicting transitive deps
    # (e.g. @azure/search-documents) and the offline build phase fails ENOTCACHED.
    # --ignore-scripts: esbuild's postinstall does a strict version-match against
    # its native binary; nixpkgs' esbuild version drifts from upstream's pin and
    # the script throws. The package's own `npm run build` still runs (driven
    # separately by buildNpmPackage), so TS→JS compilation is unaffected.
    npmFlags = ["--legacy-peer-deps" "--ignore-scripts"];
    nodejs = pkgs.nodejs_22;
  };
in {
  home.packages = [
    hubstaffMcp  # Hubstaff MCP server for time tracking (use: hubstaff-mcp)
    n8nMcp       # n8n MCP server for workflow automation (use: n8n-mcp)
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

  # n8n — workflow automation. Talks to a self-hosted or cloud n8n instance via its REST API.
  # Required env vars: N8N_API_URL (e.g. https://n8n.example.com), N8N_API_KEY (instance API key).
  # The \${VAR} escape produces literal ${VAR} in the generated JSON, which Claude expands at spawn time.
  devcell.managedMcp.servers."n8n" = {
    command = "${bin}/n8n-mcp";
    args = [];
    env = {
      N8N_API_URL = "\${N8N_API_URL}";
      N8N_API_KEY = "\${N8N_API_KEY}";
    };
  };
}
