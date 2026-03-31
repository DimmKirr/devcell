# qa-tools.nix — QA and testing MCP tools
{pkgs, config, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;
  py = pkgs.python312Packages;

  # mailslurp-client: Python SDK for MailSlurp email API (not in nixpkgs)
  mailslurpClient = py.buildPythonPackage {
    pname = "mailslurp-client";
    version = "17.3.0";
    pyproject = true;
    src = pkgs.fetchPypi {
      pname = "mailslurp_client";
      version = "17.3.0";
      hash = "sha256-HKkz22A8RURbd1CZwIseIcAUUSxd+hWgoIe4o6ztfo8=";
    };
    build-system = [py.setuptools];
    dependencies = with py; [
      urllib3
      six
      certifi
      python-dateutil
    ];
    doCheck = false;
  };

  # mailslurp-mcp: MailSlurp email testing MCP — create/find inboxes, read/list/clear emails
  # https://github.com/DimmKirr/mailslurp-mcp
  mailslurpMcp = py.buildPythonApplication {
    pname = "mailslurp-mcp";
    version = "0.1.0";
    src = pkgs.fetchFromGitHub {
      owner = "DimmKirr";
      repo = "mailslurp-mcp";
      rev = "f8f2b52414dcf1241eaa6cbca46b2b48a159e234";
      hash = "sha256-heKuX4e7k0eDoUiT+v4n6Ydh3qve5Up9fU26ejp95Tw=";
    };
    pyproject = true;
    build-system = [py.setuptools];
    postPatch = ''
      rm -rf nix docs scripts tests
    '';
    dependencies = [
      py.fastmcp
      mailslurpClient
    ];
    doCheck = false;
  };
in {
  home.packages = [
    mailslurpMcp # MailSlurp email testing MCP (use: mailslurp-mcp)
  ];

  # MailSlurp — 6 tools: create_inbox, find_inbox, get_or_create_inbox, read_email, list_emails, clear_inbox.
  # Requires MAILSLURP_API_KEY env var at runtime.
  devcell.managedMcp.servers."mailslurp" = {
    command = "${bin}/mailslurp-mcp";
    args = [];
  };
}
