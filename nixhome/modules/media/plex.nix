# plex.nix — Plex Media Server MCP tools
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.plex;
  bin = config.devcell.managedMcp.nixBinPrefix;
  py = pkgs.python312Packages;

  # plex-mcp-server: control Plex Media Server via MCP — libraries, media,
  # playlists, collections, sessions, server admin.
  # https://github.com/DimmKirr/plex-mcp-server
  plexMcp = py.buildPythonApplication {
    pname = "plex-mcp-server";
    version = "1.1.7-unstable-2026-05-23";
    src = pkgs.fetchFromGitHub {
      owner = "DimmKirr";
      repo = "plex-mcp-server";
      rev = "9e4ecfbdb5e777bcdcfedeac2e9bde8d490c95b1";
      hash = "sha256-XLKu6nlhRPr1yNMSzj0WjBK+XosRcsC3gdVNlJLXP94=";
    };
    pyproject = true;
    build-system = [py.setuptools py.wheel];
    dependencies = with py; [
      aiohttp
      mcp
      plexapi
      python-dotenv
      requests
      starlette
      uvicorn
      watchdog
      pyjwt
      cryptography
    ];
    # Upstream pins all deps with `==` — let nixpkgs versions satisfy them.
    pythonRelaxDeps = true;
    doCheck = false;
  };
in {
  options.devcell.modules.plex = {
    enable = lib.mkEnableOption "Plex Media Server MCP (libraries, media, playlists, sessions, admin)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Plex Media Server control via MCP — needs PLEX_URL + PLEX_TOKEN";
        mcpServers = [ "plex" ];
        sizeMb = 80;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      plexMcp # Plex Media Server MCP (use: plex-mcp-server)
    ];

    # Plex MCP — libraries, media, playlists, collections, sessions, admin.
    # Requires PLEX_URL (e.g. http://plex.local:32400) and PLEX_TOKEN at runtime.
    # Token: https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/
    devcell.managedMcp.servers."plex" = {
      command = "${bin}/plex-mcp-server";
      args = ["--transport" "stdio"];
      env = {
        PLEX_URL = "\${PLEX_URL}";
        PLEX_TOKEN = "\${PLEX_TOKEN}";
      };
    };
  };
}
