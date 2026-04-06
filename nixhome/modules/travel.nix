# travel.nix — Travel and geospatial tools
{pkgs, config, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;
  py = pkgs.python312Packages;

  # mcp-google-map: Google Maps MCP — 17 tools (geocoding, routing, places, elevation, air quality, timezone, etc.)
  # https://github.com/cablate/mcp-google-map
  mcpGoogleMap = pkgs.buildNpmPackage {
    pname = "mcp-google-map";
    version = "0.0.47";
    src = pkgs.fetchFromGitHub {
      owner = "cablate";
      repo = "mcp-google-map";
      rev = "88e406aadbc01e0bb68eb42e4dda373eb92b3e07";
      hash = "sha256-oe8LjhwC9yxiMVGe9BvbDWn0BnguGZph8J2JSSRGgng=";
    };
    npmDepsHash = "sha256-9ocpBEJtpPafdkKPDeM4h8FBt4hPCnDFOssvdLRvP2o=";
    nodejs = pkgs.nodejs_22;
  };

  # tripit-mcp: TripIt MCP server — list_trips, get_trip with date filtering
  # https://github.com/DimmKirr/tripit-mcp
  tripitMcp = py.buildPythonApplication {
    pname = "tripit-mcp";
    version = "0.1.0";
    src = pkgs.fetchFromGitHub {
      owner = "DimmKirr";
      repo = "tripit-mcp";
      rev = "e19a88db76da3c660b9531e974f06fc4bfbb4975";
      hash = "sha256-DJQj0Xc6mFeoT7kv6vHPlojZZdOLHSF0DhRM+K2JKVY=";
    };
    pyproject = true;
    build-system = [py.setuptools];
    # Remove non-Python dirs that confuse setuptools flat-layout discovery
    postPatch = ''
      rm -rf nix docs scripts tests
    '';
    dependencies = with py; [
      fastmcp
      httpx
      uvicorn
    ];
    doCheck = false;
  };
in {
  home.packages = [
    mcpGoogleMap # Google Maps MCP server (use: mcp-google-map)
    tripitMcp # TripIt MCP server (use: tripit-mcp)
  ];

  # Google Maps — 17 tools: geocoding, routing, places, elevation, air quality.
  # Requires GOOGLE_MAPS_API_KEY env var at runtime.
  devcell.managedMcp.servers."google-maps" = {
    command = "${bin}/mcp-google-map";
    args = ["--stdio"];
    env.GOOGLE_MAPS_API_KEY = "\${GOOGLE_MAPS_API_KEY}";
  };

  # TripIt — list_trips, get_trip with date filtering.
  # Requires TRIPIT_USERNAME, TRIPIT_PASSWORD, TRIPIT_CLIENT_ID, TRIPIT_CLIENT_SECRET env vars at runtime.
  devcell.managedMcp.servers."tripit" = {
    command = "${bin}/tripit-mcp";
    args = [];
  };
}
