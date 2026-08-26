# wireguard.nix — WireGuard VPN tunnel support for country-specific exit IPs
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.wireguard;
in {
  options.devcell.modules.wireguard = {
    enable = lib.mkEnableOption "WireGuard VPN tunnel with boringtun userspace fallback";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "WireGuard VPN tunnel + boringtun userspace fallback";
        mcpServers = [];
        sizeMb = 15;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      wireguard-tools  # wg, wg-quick (use: wg-quick up <conf>)
      boringtun        # userspace WireGuard (use: WG_QUICK_USERSPACE_IMPLEMENTATION=boringtun-cli)
    ];

    home.file.".config/devcell/entrypoint.d/26-wireguard.sh" = {
      executable = true;
      text = ''
        #!/usr/bin/env bash
        [ "''${DEVCELL_WG_ENABLED:-}" = "1" ] || return 0
        notify wireguard.starting

        WG_DIR="/home/$HOST_USER/.devcell/$DEVCELL_CELL_NAME/.wg"
        if [ ! -d "$WG_DIR" ] || [ -z "$(ls -A "$WG_DIR"/*.conf 2>/dev/null)" ]; then
          log "wireguard: no .conf files in $WG_DIR, skipping"
          notify wireguard.ready
          return 0
        fi

        if [ -z "''${WG_PRIVATE_KEY:-}" ]; then
          echo "wireguard: WG_PRIVATE_KEY must be set when wireguard is enabled" >&2
          exit 1
        fi

        echo "$WG_PRIVATE_KEY" > /run/secrets/wg-private-key
        chmod 600 /run/secrets/wg-private-key

        if [ -n "''${WG_PRESHARED_KEY:-}" ]; then
          echo "$WG_PRESHARED_KEY" > /run/secrets/wg-preshared-key
          chmod 600 /run/secrets/wg-preshared-key
        fi

        # Docker --sysctl sets src_valid_mark at creation, but /proc/sys is
        # read-only at runtime. Patch wg-quick to skip the redundant sysctl call.
        WQ_WRAPPER=$(readlink -f "$(command -v wg-quick)")
        WQ_UNWRAPPED=$(grep "exec -a" "$WQ_WRAPPER" | grep -oP '/nix/store/[^"]+' | head -1)
        cp "$WQ_UNWRAPPED" /tmp/wg-quick-patched
        sed -i 's|cmd sysctl -q net.ipv4.conf.all.src_valid_mark=1|true|' /tmp/wg-quick-patched
        chmod +x /tmp/wg-quick-patched
        sed "s|$WQ_UNWRAPPED|/tmp/wg-quick-patched|" "$WQ_WRAPPER" > /tmp/wg-quick
        chmod +x /tmp/wg-quick

        for conf in "$WG_DIR"/*.conf; do
          [ -f "$conf" ] || continue
          name=$(basename "$conf" .conf)

          dns=$(grep -oP '^\s*DNS\s*=\s*\K\S+' "$conf")
          conf_nodns="/tmp/''${name}.conf"
          grep -v '^\s*DNS\s*=' "$conf" > "$conf_nodns"

          /tmp/wg-quick up "$conf_nodns" 2>&1 | while IFS= read -r line; do log "wireguard[$name]: $line"; done
          rm -f "$conf_nodns"

          if [ -n "$dns" ]; then
            echo "nameserver $dns" > /etc/resolv.conf
            log "wireguard[$name]: DNS set to $dns"
          fi

          log "wireguard: $name up"
        done

        notify wireguard.ready
      '';
    };
  };
}
