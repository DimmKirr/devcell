#!/bin/sh
# WSL#13236 workaround: start nix-daemon manually when systemd socket
# activation is broken because WSL hardcodes /usr/bin/systemctl.
# Must run as root. The daemon AND the store-write test happen in the
# SAME process tree — WSL kills background processes when the last
# session disconnects, so a daemon started in one wsl.exe call is
# dead by the next.
set -u
SOCKET=/nix/var/nix/daemon-socket/socket
DAEMON=/run/current-system/sw/bin/nix-daemon
TARGET_USER=${1:-nixos}

rm -f "$SOCKET"
"$DAEMON" &
i=0
while [ $i -lt 60 ]; do
    test -S "$SOCKET" && break
    sleep 1
    i=$((i+1))
done
if ! test -S "$SOCKET"; then
    echo "SOCKET_FAIL"
    exit 1
fi
echo "SOCKET_OK"
sleep 5
su - "$TARGET_USER" -c 'timeout 30 nix-store --add /etc/hostname 2>&1'
echo "STORE_EXIT=$?"
