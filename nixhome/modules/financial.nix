# financial.nix — Financial data MCP servers
{pkgs, config, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;
  py = pkgs.python312Packages;

  # httpxthrottlecache: rate-limiting + caching httpx wrapper (edgartools dep, not in nixpkgs)
  httpxthrottlecache = py.buildPythonPackage {
    pname = "httpxthrottlecache";
    version = "0.3.5";
    src = pkgs.fetchPypi {
      pname = "httpxthrottlecache";
      version = "0.3.5";
      hash = "sha256-7aNhG5/L8eIEYZFpX7koVfc/wzOnwEDTZgqsEbmmDB8=";
    };
    pyproject = true;
    build-system = [py.hatchling];
    postPatch = ''
      substituteInPlace pyproject.toml \
        --replace-fail 'dynamic = ["version"]' 'version = "0.3.5"'
      sed -i '/uv-dynamic-versioning/d' pyproject.toml
    '';
    dependencies = with py; [aiofiles filelock httpx pyrate-limiter];
    pythonRelaxDeps = ["pyrate-limiter"]; # nixpkgs has 3.9.0, needs >=4.0.0
    doCheck = false;
  };

  # yahoo-finance-mcp: stock prices, financials, news, options via yfinance (no API key)
  yahooFinanceMcp = py.buildPythonApplication {
    pname = "yahoo-finance-mcp";
    version = "0-unstable-2026-03-19";
    src = pkgs.fetchFromGitHub {
      owner = "Alex2Yang97";
      repo = "yahoo-finance-mcp";
      rev = "81dcf33d69a8808696820e55373028741584b1ae";
      hash = "sha256-QKizB4zU9KfojK6wkyEtXfp8Pl+htlJCZmO2YHhSfnY=";
    };
    pyproject = true;
    build-system = [py.hatchling];
    dependencies = with py; [mcp yfinance];
    doCheck = false;
  };

  # edgartools: SEC filings (10-K, 10-Q, 8-K, 13F), XBRL financials, insider trades (no API key)
  # Needs SEC_EDGAR_IDENTITY="Name email@example.com" env var at runtime.
  edgartoolsMcp = py.buildPythonApplication {
    pname = "edgartools";
    version = "0-unstable-2026-03-19";
    src = pkgs.fetchFromGitHub {
      owner = "dgunning";
      repo = "edgartools";
      rev = "07060ec91d4d2d2a14020f3eb76a7a0b46a7eab3";
      hash = "sha256-3+Ji/zFmhzixugpTiruAN8zJKYEc14Vbw0dHyocQoe8=";
    };
    pyproject = true;
    build-system = [py.hatchling];
    dependencies = with py; [
      # core
      httpx pandas tabulate pyarrow beautifulsoup4 lxml rich humanize
      stamina orjson textdistance rank-bm25 rapidfuzz unidecode
      pydantic tqdm nest-asyncio jinja2 pyrate-limiter truststore
      httpxthrottlecache
      # ai extras (needed for MCP server)
      mcp tiktoken starlette uvicorn
    ];
    doCheck = false;
  };

  # mcp-fredapi: 800K+ FRED economic time series — GDP, CPI, rates, etc.
  # Requires FRED_API_KEY env var (free at https://fred.stlouisfed.org/docs/api/api_key.html)
  # No [build-system] in upstream pyproject.toml — use writeShellScriptBin wrapper.
  fredapiSrc = pkgs.fetchFromGitHub {
    owner = "Jaldekoa";
    repo = "mcp-fredapi";
    rev = "000208215cedfc523ef98336db77c54c47ddc2c0";
    hash = "sha256-JdKfjrXKfWNPCtRB6G371hCM2UfKx3PubIbYmQXYv4Y=";
  };
  fredapiMcp = pkgs.writeShellScriptBin "mcp-fredapi" ''
    exec ${
      (pkgs.python312.withPackages (ps: with ps; [httpx mcp python-dotenv pydantic]))
    }/bin/python ${fredapiSrc}/server.py "$@"
  '';
in {
  home.packages = [
    yahooFinanceMcp # Yahoo Finance MCP (stocks, news, options — no API key)
    edgartoolsMcp # SEC EDGAR MCP (filings, financials — no API key)
    fredapiMcp # FRED MCP (economic data — free API key)
  ];

  devcell.managedMcp.servers."yahoo-finance" = {
    command = "${bin}/yahoo-finance-mcp";
    args = [];
  };

  devcell.managedMcp.servers."edgartools" = {
    command = "${bin}/edgartools-mcp";
    args = [];
    # Set SEC_EDGAR_IDENTITY="Your Name your@email.com" in environment
  };

  devcell.managedMcp.servers."mcp-fredapi" = {
    command = "${bin}/mcp-fredapi";
    args = [];
    # Set FRED_API_KEY in environment (free: https://fred.stlouisfed.org/docs/api/api_key.html)
  };
}
