#!/bin/bash
# 22-chromium-singleton.sh — clear stale Chromium singleton lock files (CELL-74).
# Sourced by entrypoint.sh as root, BEFORE the gosu drop to HOST_USER.
#
# WHY
# ---
# Chromium creates three singleton-coordination files in its user-data-dir:
#   SingletonLock    — symlink → "<hostname>-<pid>" of the owning process
#   SingletonCookie  — symlink → "<random>-<unix-time>"
#   SingletonSocket  — unix socket for second-instance handoff
# When chromium exits gracefully these are cleaned up. When it's killed by
# SIGKILL (e.g. `docker rm -f`, OOM, container restart, panic) they're left
# behind, pointing at a PID that's now dead in the previous PID namespace.
# A subsequent chromium launch either hangs probing the dead socket, or
# refuses to start with "Failed to acquire SingletonLock".
#
# WHAT
# ----
# On every container start, sweep these three files from ALL per-app
# Chromium profile dirs under $HOME/.chrome/. We don't need to check
# whether the owner is alive — by PID-namespace isolation, any PID
# recorded in a SingletonLock from a previous container generation is
# DEFINITIVELY dead. Unconditional `find -delete` is safe.
#
# This fragment exists specifically because Phase 2 of CELL-74 unifies
# interactive chromium + MCP automation onto a single per-app profile;
# without singleton cleanup, that sharing produces fragile boot behaviour.

notify chromium.starting

# Profile root may not exist on first launch — find tolerates this.
if [ -d "$HOME/.chrome" ]; then
    # -maxdepth 3 because layout is $HOME/.chrome/<app>/Singleton* (depth 3).
    # Wider depth risks matching unrelated nested data. Errors silenced so a
    # missing profile dir or a transient mid-restart file doesn't fail the
    # fragment.
    find "$HOME/.chrome" -maxdepth 3 \( \
        -name SingletonLock -o \
        -name SingletonCookie -o \
        -name SingletonSocket \
    \) -delete 2>/dev/null || true
fi

# Chromium also drops `$HOME/tmp/.org.chromium.Chromium.<rand>/SingletonSocket`
# orphans on crash — the actual unix socket file the SingletonSocket symlink
# in the user-data-dir points at. The above find only touches user-data-dirs.
# These accumulate across container restarts on a persistent $HOME mount; PID
# namespace isolation makes any orphan from a previous generation definitely
# dead, so unconditional sweep is safe (CELL-62).
if [ -d "$HOME/tmp" ]; then
    find "$HOME/tmp" -maxdepth 1 -type d -name '.org.chromium.Chromium.*' \
        -exec rm -rf {} + 2>/dev/null || true
fi

notify chromium.ready
