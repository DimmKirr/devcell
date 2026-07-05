# security.nix — web security scanning and vulnerability discovery tools
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.security;
  bin = config.devcell.managedMcp.nixBinPrefix;

  # ── hexstrike-ai: Security audit MCP server — 150+ tools ──────────────────
  # https://github.com/0x4m4/hexstrike-ai
  # Two-process architecture: Flask API server + MCP stdio client.
  # The wrapper starts the server in the background, waits for /health, then
  # runs the MCP client in foreground (stdio transport).
  hexstrikeSrc = pkgs.fetchFromGitHub {
    owner = "0x4m4";
    repo = "hexstrike-ai";
    rev = "83337796dcfb8cfbf733bd24d0b2c7e4f0732790";
    hash = "sha256-WETztqhUTyeIEpUjMM4j4voGpVAiIVWlTiOozViVXVU=";
  };

  # Server deps (top-level imports only — pwntools/angr are template strings, not real imports)
  hexstrikePython = pkgs.python312.withPackages (ps: with ps; [
    flask requests psutil aiohttp
    beautifulsoup4 selenium mitmproxy
    mcp  # provides mcp.server.fastmcp.FastMCP
  ]);

  hexstrikeMcp = pkgs.writeShellScriptBin "hexstrike-mcp" ''
    set -euo pipefail
    PORT=''${HEXSTRIKE_PORT:-8888}

    # Start Flask API server in background.
    # Upstream writes hexstrike.log via FileHandler — cd to /tmp to avoid polluting project dir.
    (cd /tmp && ${hexstrikePython}/bin/python3 ${hexstrikeSrc}/hexstrike_server.py --port "$PORT") &
    SERVER_PID=$!
    trap "kill $SERVER_PID 2>/dev/null; wait $SERVER_PID 2>/dev/null" EXIT

    # Wait for /health (up to 15s)
    for _ in $(seq 1 30); do
      if ${pkgs.curl}/bin/curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then break; fi
      sleep 0.5
    done

    # Run MCP client (stdio)
    exec ${hexstrikePython}/bin/python3 ${hexstrikeSrc}/hexstrike_mcp.py --server "http://127.0.0.1:$PORT"
  '';

  hexstrikeServer = pkgs.writeShellScriptBin "hexstrike-server" ''
    exec ${hexstrikePython}/bin/python3 ${hexstrikeSrc}/hexstrike_server.py "$@"
  '';

  # ── wappalyzergo: Go library for web technology fingerprinting ─────────────
  # https://github.com/projectdiscovery/wappalyzergo
  # Library used internally by httpx -tech-detect. Builds cmd/update-fingerprints.
  wappalyzergo = pkgs.buildGoModule rec {
    pname = "wappalyzergo";
    version = "0.2.73";
    src = pkgs.fetchFromGitHub {
      owner = "projectdiscovery";
      repo = "wappalyzergo";
      rev = "v${version}";
      hash = "sha256-ECoB8eKVZ0+OFn5xfQ5KnXV0YM63m4ztBWbpl48OpHE=";
    };
    vendorHash = "sha256-HTh1iNGQXmYe9eNEBhZixr8jyBqWsKhTcUHX4vzItIU=";
    subPackages = ["cmd/update-fingerprints"];
    meta = with lib; {
      description = "Wappalyzer technology detection library for Go";
      homepage = "https://github.com/projectdiscovery/wappalyzergo";
      license = licenses.mit;
    };
  };
in {
  options.devcell.modules.security = {
    enable = lib.mkEnableOption "150+ web/binary security scanning tools (nuclei, nikto, sqlmap, ghidra, radare2, ...)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Vuln scanners + fuzzers + recon + RE + forensics (nuclei, nmap, sqlmap, ghidra, ...)";
        mcpServers = [ ];  # hexstrike-ai disabled upstream
        sizeMb = 3500;
      };
    };
  };

  config = lib.mkIf cfg.enable {
  home.packages = with pkgs; [
    # vulnerability scanners
    nuclei            # template-based vuln scanner (use: nuclei -u https://target.com)
    nikto             # web server scanner — misconfigs, default files, headers (use: nikto -h target.com)
    sqlmap            # SQL injection detection + exploitation (use: sqlmap -u "url?id=1")
    dalfox            # XSS vulnerability scanner (use: dalfox url "https://target.com?q=test")

    # fuzzers & brute-forcing
    ffuf              # fast web fuzzer — dirs, params, vhosts (use: ffuf -u URL/FUZZ -w wordlist)
    gobuster          # directory/DNS/vhost brute-forcing (use: gobuster dir -u URL -w wordlist)

    # reconnaissance & crawling
    httpx             # HTTP probing — tech detection, status codes (use: httpx -u target.com -tech-detect)
    katana            # web crawler — JS endpoint discovery (use: katana -u https://target.com)
    subfinder         # passive subdomain discovery (use: subfinder -d target.com)
    whatweb           # technology fingerprinting (use: whatweb target.com)
    wafw00f           # WAF fingerprinting (use: wafw00f https://target.com)
    nmap              # port scanner + NSE vuln scripts (use: nmap -sV --script=vuln target.com)

    # mobile app analysis
    apkeep            # APK downloader from Google Play / APKPure (use: apkeep -a com.example.app .)
    jadx              # APK/DEX decompiler → readable Java source (use: jadx app.apk -d out/)

    # binary analysis & reverse engineering (PE / ELF / Mach-O)
    ghidra                      # NSA RE suite — best-in-class PE decompiler (use: ghidra)
    radare2                     # swiss-army RE framework, strong PE support (use: r2 file.exe)
    rizin                       # radare2 fork, cleaner codebase (use: rizin file.exe)
    binwalk                     # firmware/binary analyzer + carving (use: binwalk -e file.exe)
    yara                        # pattern matching for malware/PE ID (use: yara rules.yar file.exe)
    upx                         # executable packer/unpacker (use: upx -d file.exe)
    pev                         # PE-specific toolkit: pestr/pesec/pedis/pescan (use: readpe file.exe)
    detect-it-easy              # PE compiler/packer/protector ID (use: diec file.exe)
    capstone                    # multi-arch disassembly engine (lib + cstool: cstool x86 …)
    python312Packages.ropper    # ROP gadget finder for exploit dev (use: ropper -f file.exe)

    # forensics & file carving
    foremost                    # file carving by header/footer signatures (use: foremost -i image.dd -o out/)
    sleuthkit                   # disk image / filesystem forensics suite (use: fls, icat, mmls, fsstat)

    # parameter discovery
    arjun             # HTTP parameter discovery (use: arjun -u https://target.com/endpoint)

    # wordlists & template databases
    # seclists omitted (~1.9GB) — download on-demand: nix run nixpkgs#seclists
    nuclei-templates  # ProjectDiscovery vulnerability templates (~66MB)

    # technology fingerprinting (Go library + update-fingerprints tool)
    wappalyzergo      # Wappalyzer Go impl (use: update-fingerprints)

    # MCP security audit server — disabled, source build broken upstream
    # hexstrikeMcp      # hexstrike-ai MCP wrapper (use: hexstrike-mcp)
    # hexstrikeServer   # hexstrike-ai Flask server (use: hexstrike-server)
  ];

  # ── Wordlist symlinks for hexstrike/ffuf/gobuster ────────────────────────
  # seclists removed from image (~1.9GB). Users can install on-demand:
  #   nix profile install nixpkgs#seclists
  # Then re-run this activation to create symlinks:
  #   home-manager switch --flake /opt/nixhome#devcell-ultimate
  home.activation.wordlistSymlinks = lib.hm.dag.entryAfter ["writeBoundary"] ''
    export PATH="/usr/bin:/bin:$PATH"
    # nixos/nix:latest has /usr/share as a symlink into the nix store
    if [ -L /usr/share ]; then
      $DRY_RUN_CMD sudo rm /usr/share
      $DRY_RUN_CMD sudo mkdir -p /usr/share
    fi
    $DRY_RUN_CMD sudo mkdir -p /usr/share/wordlists
    # seclists symlinks — only created if seclists is installed
    if [ -d "${pkgs.dirb}/share/dirb/wordlists" ]; then
      $DRY_RUN_CMD sudo ln -sfT ${pkgs.dirb}/share/dirb/wordlists /usr/share/wordlists/dirb
    fi
  '';

  # HexStrike AI — 150+ security audit tools via MCP.
  # Two-process: Flask API + MCP stdio client, started together by the wrapper.
  # devcell.managedMcp.servers."hexstrike-ai" = {
  #   command = "${bin}/hexstrike-mcp";
  #   args = [];
  # };
  };
}