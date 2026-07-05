# financial.nix — Financial data MCP servers
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.financial;
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

  # firefly-iii-mcp: Firefly III (self-hosted personal finance manager) — full
  # OpenAPI-generated tool set, including `store_rule` with proper triggers[] +
  # actions[] arrays (set_category, add_tag, set_destination_account, etc.).
  # https://github.com/etnperlong/firefly-iii-mcp
  # Requires FIREFLY_III_BASE_URL + FIREFLY_III_PAT (Profile → OAuth → Personal
  # Access Tokens).
  #
  # NOTE on the post-install patch:
  # v1.4.0 ships 28 tools whose JSON Schema property keys contain PHP-style array
  # suffixes (`accounts[]`, `tags[]`, etc.) lifted verbatim from the Firefly
  # OpenAPI spec. Anthropic's tool API rejects keys not matching
  # `^[a-zA-Z0-9_.-]{1,64}$`, and a single bracketed key fails the whole tools
  # array with HTTP 400 — making the MCP unusable from Claude Desktop and any
  # eager-loading MCP client. Tracked upstream as
  # https://github.com/etnperlong/firefly-iii-mcp/issues/6 (no fix yet).
  # The wrapper installs the package into a stable cache dir on first run and
  # strips those 28 broken entries (all `insight_*` + `test_rule`/`fire_rule`/
  # `test_rule_group`/`fire_rule_group`) from `generatedTools` before the
  # server loads it. Survives until the user clears ~/.cache/firefly-iii-mcp.
  fireflyPatchScript = pkgs.writeText "firefly-iii-mcp-patch.mjs" ''
    import fs from 'fs';
    import path from 'path';
    import { pathToFileURL } from 'url';

    const pkgDir = process.argv[2];
    const toolsPath = path.join(pkgDir, 'node_modules/@firefly-iii-mcp/core/dist/tools.js');
    const mod = await import(pathToFileURL(toolsPath).href);

    const ANTHROPIC_KEY_RE = /^[a-zA-Z0-9_.-]{1,64}$/;
    function isClean(o) {
      if (!o || typeof o !== 'object') return true;
      if (o.properties) {
        for (const k of Object.keys(o.properties)) {
          if (!ANTHROPIC_KEY_RE.test(k)) return false;
          if (!isClean(o.properties[k])) return false;
        }
      }
      if (o.items && !isClean(o.items)) return false;
      return true;
    }

    const before = mod.generatedTools.length;
    const dropped = mod.generatedTools.filter(t => !isClean(t.inputSchema)).map(t => t.name);
    const filtered = mod.generatedTools.filter(t => isClean(t.inputSchema));
    fs.writeFileSync(toolsPath,
      'export const generatedTools = ' + JSON.stringify(filtered) + ';\n');
    console.error('[firefly-iii-mcp patch] kept ' + filtered.length + '/' + before +
      ' tools; dropped ' + dropped.length + ' with invalid schema keys: ' + dropped.join(', '));
  '';

  fireflyIiiMcp = pkgs.writeShellScriptBin "firefly-iii-mcp" ''
    set -e
    CACHE_DIR="$HOME/.cache/firefly-iii-mcp/1.4.0"
    PKG_PATH="$CACHE_DIR/node_modules/@firefly-iii-mcp/local"
    SENTINEL="$CACHE_DIR/.patched-v1"
    NODE=${pkgs.nodejs_22}/bin/node
    NPM=${pkgs.nodejs_22}/bin/npm

    if [ ! -f "$SENTINEL" ]; then
      mkdir -p "$CACHE_DIR"
      [ -f "$CACHE_DIR/package.json" ] || echo '{}' > "$CACHE_DIR/package.json"
      (cd "$CACHE_DIR" && "$NPM" install --silent --no-audit --no-fund \
        @firefly-iii-mcp/local@1.4.0 1>&2)
      "$NODE" ${fireflyPatchScript} "$CACHE_DIR"
      touch "$SENTINEL"
    fi
    exec "$NODE" "$PKG_PATH/dist/app.js" "$@"
  '';
in {
  options.devcell.modules.financial = {
    enable = lib.mkEnableOption "Yahoo Finance + SEC EDGAR + FRED + Firefly III MCP servers (financial data)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Yahoo Finance, SEC EDGAR, FRED, Firefly III — market data, filings, economic time series, personal finance";
        mcpServers = [ "yahoo-finance" "edgartools" "mcp-fredapi" "firefly-iii" ];
        sizeMb = 500;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      pkgs.stripe-cli # Stripe API CLI (use: stripe listen, stripe trigger)
      yahooFinanceMcp # Yahoo Finance MCP (stocks, news, options — no API key)
      edgartoolsMcp # SEC EDGAR MCP (filings, financials — no API key)
      fredapiMcp # FRED MCP (economic data — free API key)
      fireflyIiiMcp # Firefly III MCP (self-hosted personal finance — PAT required)
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

    # Firefly III — full OpenAPI tool set (accounts, transactions, budgets, categories,
    # tags, bills, rules with triggers[] + actions[], rule_groups, recurrences, webhooks).
    # Requires FIREFLY_URL (e.g. http://firefly.lan:8080) + FIREFLY_TOKEN (Personal Access
    # Token) at runtime — generate at Profile → OAuth → Personal Access Tokens. Existing
    # env var names are remapped to the upstream FIREFLY_III_* names below.
    devcell.managedMcp.servers."firefly-iii" = {
      command = "${bin}/firefly-iii-mcp";
      args = [];
      env.FIREFLY_III_BASE_URL = "\${FIREFLY_URL}";
      env.FIREFLY_III_PAT = "\${FIREFLY_TOKEN}";
      env.FIREFLY_III_PRESET = "full";
    };
  };
}
