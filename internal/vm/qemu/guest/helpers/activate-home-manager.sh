#!/bin/sh
# Activate nixhome via home-manager inside NixOS-WSL. Runs as root, and the
# WHOLE sequence shares this one process tree: WSL kills background processes
# when the wsl.exe session ends, so the nix-daemon that nix-verify started is
# dead by now — it must be ensured here, beside the switch that needs it
# (WSL#13236; proven run 20260804).
#
# Usage: activate-home-manager.sh <user> <tarball-path> [flake-attr]
#   user         distro user the flake was built for (nixhome pins "nixos")
#   tarball-path nixhome.tgz as seen inside WSL (/mnt/<vol>/devcell/nixhome.tgz)
#   flake-attr   optional; defaults to wsl-base with the arch suffix
#
# nixhome travels as a TARBALL and is extracted to ext4: activating from the
# share path fails on nix's dirty-git-tree ingestion and on readlink over
# virtiofs+drvfs (36 symlinks in the icewm theme, run 20260804).
set -u
USER_NAME=${1:-nixos}
TARBALL=${2:?tarball path required}
ATTR=${3:-}

SOCKET=/nix/var/nix/daemon-socket/socket
DAEMON=/run/current-system/sw/bin/nix-daemon

if ! su - "$USER_NAME" -c 'timeout 30 nix-store --add /etc/hostname' >/dev/null 2>&1; then
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
su - "$USER_NAME" -c 'timeout 60 nix-store --add /etc/hostname' >/dev/null || { echo "STORE_FAIL"; exit 1; }
echo "STORE_OK"

su - "$USER_NAME" -c 'mkdir -p ~/.config/nix && printf "experimental-features = nix-command flakes\n" > ~/.config/nix/nix.conf'

SRC="/home/$USER_NAME/nixhome-src"
rm -rf "$SRC"
mkdir -p "$SRC"
tar -xzf "$TARBALL" -C "$SRC" || { echo "TAR_FAIL"; exit 1; }
chown -R "$USER_NAME": "$SRC"
echo "NIXHOME_COPIED"

if [ -z "$ATTR" ]; then
    SUFFIX=$([ "$(uname -m)" = "aarch64" ] && echo "-aarch64" || echo "")
    ATTR="wsl-base$SUFFIX"
fi

echo "HM_START attr=$ATTR"
su - "$USER_NAME" -c "cd $SRC/nixhome && nix --extra-experimental-features nix-command --extra-experimental-features flakes run home-manager/release-26.05 -- switch -b backup --flake .#$ATTR > /tmp/devcell-hm-activate.log 2>&1; rc=\$?; tail -40 /tmp/devcell-hm-activate.log; exit \$rc"
rc=$?
echo "HM_EXIT=$rc"
exit $rc
