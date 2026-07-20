# social.nix — Fediverse / social media tools (Mastodon)
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.social;
  bin = config.devcell.managedMcp.nixBinPrefix;
  py = pkgs.python312Packages;

  # mastodon-mcp-server: Mastodon MCP — timelines, search, post, accounts, notifications
  # https://github.com/VitexSoftware/mastodon-mcp-server
  mastodonMcp = py.buildPythonApplication {
    pname = "mastodon-mcp-server";
    version = "1.0.1-unstable-2026-07-03";
    src = pkgs.fetchFromGitHub {
      owner = "VitexSoftware";
      repo = "mastodon-mcp-server";
      rev = "a3ac361bd5aa5bdb2ba29747091ee86c9700f0bf";
      hash = "sha256-mQixeDnPqGABiy8LWehpzdHNTPN+QZBNyWWDo41qiKM=";
    };
    pyproject = true;
    build-system = [py.setuptools];
    postPatch = ''
      rm -rf debian scripts
    '';
    dependencies = with py; [
      mastodon-py
      python-dotenv
    ];
    doCheck = false;
  };
in {
  options.devcell.modules.social = {
    enable = lib.mkEnableOption "Mastodon / Fediverse MCP";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Mastodon — timelines, search, post, accounts, notifications";
        mcpServers = [ "mastodon" ];
        sizeMb = 40;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      mastodonMcp # Mastodon MCP server (use: mastodon-mcp)
    ];

    # Mastodon — timelines, search, post, accounts, notifications.
    # Requires MASTODON_INSTANCE (e.g. https://mastodon.social) and MASTODON_ACCESS_TOKEN env vars.
    # Get token: Preferences → Development → New application → copy access token.
    devcell.managedMcp.servers."mastodon" = {
      command = "${bin}/mastodon-mcp";
      args = [];
      env = {
        MASTODON_INSTANCE = "\${MASTODON_INSTANCE}";
        MASTODON_ACCESS_TOKEN = "\${MASTODON_ACCESS_TOKEN}";
      };
    };
  };
}
