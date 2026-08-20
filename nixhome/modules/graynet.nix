# graynet.nix — Tor network access for .onion routing and anonymous crawling
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.graynet;
in {
  options.devcell.modules.graynet = {
    enable = lib.mkEnableOption "Tor SOCKS proxy + torsocks for .onion access";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Tor SOCKS proxy + torsocks wrapper + nyx monitor";
        mcpServers = [];
        sizeMb = 80;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      tor               # SOCKS5 proxy daemon on :9050 (use: tor)
      torsocks          # LD_PRELOAD TCP-through-Tor wrapper (use: torsocks curl ...)
      nyx               # terminal Tor status monitor (use: nyx)
    ];

    home.file.".config/tor/torrc".text = ''
      SocksPort 9050
      Log notice stderr
      DataDirectory /tmp/tor-data
    '';

    home.file.".config/devcell/entrypoint.d/25-tor.sh" = {
      executable = true;
      text = ''
        #!/usr/bin/env bash
        notify graynet.starting
        mkdir -p /tmp/tor-data
        if ! tor -f "''${DEVCELL_HOME:-/opt/devcell}/.config/tor/torrc" --RunAsDaemon 1; then
          echo "graynet: tor failed to start" >&2
          exit 1
        fi
        notify graynet.ready
      '';
    };
  };
}
