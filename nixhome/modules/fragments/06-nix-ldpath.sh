#!/bin/bash
# 06-nix-ldpath.sh — export LD_LIBRARY_PATH from full nix profile closure
# Sourced by entrypoint.sh before 50-gui.sh. All services inherit this.
_NLD="/opt/devcell/.nix-ld-library-path"
if [ -f "$_NLD" ]; then
  export LD_LIBRARY_PATH="$(cat "$_NLD")${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi
