#!/bin/sh
# Build the next NixOS generation ("boot", never "switch" — the upstream
# change-username procedure is explicit) inside NixOS-WSL. Runs as root.
#
# Two image-specific traps, both proven on 20260805:
#  - root's LOCAL store mode is broken: eval dies with
#    `opening lock file /nix/var/nix/temproots/<pid>: No such file or
#    directory` even after pre-creating the directory (3 runs). Every
#    daemon-mediated operation worked, so the rebuild runs NIX_REMOTE=daemon.
#  - the daemon itself must be ensured HERE: WSL kills background processes
#    per wsl.exe session (WSL#13236), so no earlier stage's daemon survives.
#
# Usage: nixos-rebuild-boot.sh [probe-user]
#   probe-user  unprivileged user for the daemon probe (default nixos); root
#               passes in local mode even when the daemon is dead.
set -u
PROBE_USER=${1:-nixos}

SOCKET=/nix/var/nix/daemon-socket/socket
DAEMON=/run/current-system/sw/bin/nix-daemon

if ! su - "$PROBE_USER" -c 'timeout 30 nix-store --add /etc/hostname' >/dev/null 2>&1; then
    echo "nix-daemon not answering - starting manually (WSL#13236)"
    rm -f "$SOCKET"
    "$DAEMON" &
    i=0
    while [ $i -lt 60 ]; do
        test -S "$SOCKET" && break
        sleep 1
        i=$((i+1))
    done
    test -S "$SOCKET" || { echo "SOCKET_FAIL"; exit 1; }
    echo "SOCKET_OK"
    sleep 3
fi
echo "STORE_OK"

export PATH="/run/current-system/sw/bin:$PATH"
# The image ships without the temproots dir; harmless to pre-create.
mkdir -p /nix/var/nix/temproots
export NIX_REMOTE=daemon
nixos-rebuild boot > /tmp/devcell-rebuild.log 2>&1
rc=$?
tail -20 /tmp/devcell-rebuild.log
echo "REBUILD_EXIT=$rc"
exit $rc
