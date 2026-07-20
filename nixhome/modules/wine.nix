# wine.nix — Wine + Wails3 Windows app runtime
# Provides Wine (staging) with winetricks and dependencies needed to run
# Wails v3 applications compiled for Windows. Wails3 uses WebView2
# (Chromium-based) on Windows; under Wine the WebView2 runtime must be
# installed via winetricks into the Wine prefix.
{
  pkgs,
  pkgsUnstable,
  lib,
  config,
  ...
}: let
  cfg = config.devcell.modules.wine;
in {
  options.devcell.modules.wine = {
    enable = lib.mkEnableOption "Wine + Wails3 Windows app runtime (staging Wine, winetricks, WebView2 support)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Wine (staging) + winetricks + deps for running Wails3 Windows apps under Wine";
        mcpServers = [];
        sizeMb = 1800;
      };
    };
  };

  config = lib.mkIf (cfg.enable && pkgs.stdenv.isLinux) {
    home.packages = [
      # ── Wine (staging from nixpkgs-unstable for best WebView2 compatibility) ──
      # stagingFull = staging patches + full feature set (OpenGL, Vulkan, gstreamer,
      # audio). Staging has better WebView2/Edge compatibility than vanilla Wine.
      pkgsUnstable.wine64Packages.stagingFull  # 64-bit Wine staging with full features (use: wine64)

      # ── Winetricks — installs Windows components into a Wine prefix ──
      # Used to bootstrap a prefix with Visual C++ Redistributable and
      # WebView2 Runtime that Wails3 Windows apps depend on.
      pkgsUnstable.winetricks  # Windows component installer (use: winetricks vcrun2022)

      # ── Winetricks dependencies ──
      pkgs.cabextract  # extract Microsoft .cab archives (winetricks dep)
      pkgs.p7zip       # 7z archive support (winetricks dep for some installers)
      pkgs.wget        # HTTP downloads (winetricks dep)

      # ── Wails3 Windows app runtime deps ──────────────────────────────────
      # Wails3 uses WebView2 (Edge/Chromium-based) on Windows. Under Wine:
      # 1. Visual C++ Redistributable: `winetricks vcrun2022`
      # 2. WebView2 Runtime: manually install the standalone installer
      # 3. .NET Framework (if plugins need it): `winetricks dotnet48`
      #
      # Wails3 cross-compilation needs only Go (already in go module):
      #   GOOS=windows GOARCH=amd64 go build
      #
      # Host-side libs that Wine may dlopen for rendering and multimedia:
      pkgs.vulkan-loader  # Vulkan ICD loader — Wine's D3D→Vulkan translation (DXVK/WineD3D)
      pkgs.freetype       # font rendering — Wine renders Windows fonts through FreeType
    ];

    # ── Wine prefix initialization helper ──────────────────────────────────
    # Creates a ready-to-use Wine prefix with Wails3 dependencies installed.
    # Usage: devcell-wine-init [prefix-path]
    # Default prefix: ~/.wine-wails3
    home.file.".local/bin/devcell-wine-init" = {
      executable = true;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail
        PREFIX="''${1:-$HOME/.wine-wails3}"
        export WINEPREFIX="$PREFIX"
        export WINEARCH=win64

        echo "==> Initializing Wine prefix at $PREFIX"
        ${pkgsUnstable.wine64Packages.stagingFull}/bin/wineboot --init

        echo "==> Installing Visual C++ 2022 Redistributable"
        ${pkgsUnstable.winetricks}/bin/winetricks -q vcrun2022

        echo "==> Wine prefix ready at $PREFIX"
        echo "    Run your Wails3 app: WINEPREFIX=$PREFIX wine64 your-app.exe"
      '';
    };
  };
}
