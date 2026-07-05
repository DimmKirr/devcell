#!/bin/bash
# 04-nix-daemon.sh — privileged image-fixups (always) + optional nix-daemon spawn.
#
# Sourced by entrypoint.sh while still running as root, BEFORE the gosu
# drop to HOST_USER.
#
# Two parts:
#
#   ALWAYS RUN (every container start):
#     - chmod 1777 /tmp           — n2c bakes /tmp at 0555, kills Xvfb lockfile,
#                                    breaks the entire GUI chain (Xvfb→x11vnc→xrdp).
#     - chmod u+s on real sudo    — nix store paths are 0555, no setuid. Without
#                                    this, `sudo` errors "must be owned by uid 0
#                                    and have the setuid bit set" — sudo-from-cell
#                                    is broken in fresh pure containers.
#     - /nix/var/nix state dirs   — needed by any nix CLI invocation (incl. read
#                                    paths like `nix-store -q`), regardless of
#                                    whether the daemon is up.
#
#   DEFAULT ON, opt out with DEVCELL_NIX_DAEMON=false:
#     - Spawn nix-daemon          — required for session user to run
#                                    `nix profile add`, `nix shell`, etc.
#                                    Without it, every nix CLI call trips
#                                    "could not set permissions on
#                                    /nix/var/nix/profiles/per-user to 755"
#                                    because the non-root user cannot chmod
#                                    the root-owned per-user dir.

notify nix.starting

# ── ALWAYS: /tmp must be 1777 (sticky world-writable) ───────────────────
# Even when DEVCELL_NIX_DAEMON is off, every GUI service needs /tmp writable.
# Xvfb fails to create /tmp/.X99-lock → no display → x11vnc has nothing to
# proxy → xrdp returns "Error connecting to user session". This is the
# single most load-bearing chmod in the entrypoint.
chmod 1777 /tmp 2>/dev/null || true

# ── ALWAYS: setuid sudo (nix store paths are 0555) ──────────────────────
# sudo is symlinked into /bin AND /sbin via buildEnv pathsToLink — both
# links resolve to the same /nix/store/...-sudo/bin/sudo target via
# readlink -f. Touch each link path defensively in case the symlink graph
# changes in a future nixpkgs revision.
_chmod_setuid_target() {
    local _link="$1"
    [ -e "$_link" ] || return 0
    local _real
    _real=$(readlink -f "$_link" 2>/dev/null) || return 0
    [ -e "$_real" ] || return 0
    if [ -u "$_real" ]; then
        log "  $_real already has setuid bit"
        return 0
    fi
    if chmod u+s "$_real" 2>/dev/null; then
        log "  setuid+ on $_real (via $_link)"
    else
        log "  ⚠ chmod u+s $_real failed (errno=$?)"
    fi
}
log "Fixing setuid bit on sudo..."
_chmod_setuid_target /bin/sudo
_chmod_setuid_target /sbin/sudo
_chmod_setuid_target /usr/bin/sudo
# gosu must NOT have setuid — gosu 1.19+ refuses to run if setuid is detected.
# All gosu calls in entrypoint fragments already run as root (pid 1), so no
# setuid is needed. For session-user privilege escalation, use sudo instead.

# ── ALWAYS: /nix/var/nix state dirs ─────────────────────────────────────
# 1777 (sticky world-writable, like /tmp): any user can create their own
# subdir, can't touch others'. Standard for multi-user nix. Needed for
# every nix CLI invocation, not just daemon mode.
log "Creating /nix/var/nix state dirs..."
mkdir -p /nix/var/nix/profiles/per-user \
         /nix/var/nix/gcroots/per-user \
         /nix/var/nix/gcroots/auto \
         /nix/var/nix/gcroots/tmp \
         /nix/var/nix/daemon-socket \
         /nix/var/nix/temproots \
         /nix/var/nix/userpool 2>/dev/null
chmod 1777 \
    /nix/var/nix/profiles/per-user \
    /nix/var/nix/gcroots/per-user \
    /nix/var/nix/temproots \
    /nix/var/nix/userpool 2>/dev/null || true
chmod 0755 /nix/var/nix/daemon-socket 2>/dev/null || true

# ── DEFAULT ON: nix-daemon spawn ───────────────────────────────────────
# Spawn the daemon unless explicitly opted out. The fixups above run
# either way, so opting out still leaves working sudo / writable /tmp.
if [ "${DEVCELL_NIX_DAEMON:-true}" != "true" ]; then
    log "nix-daemon disabled via DEVCELL_NIX_DAEMON=$DEVCELL_NIX_DAEMON"
    notify nix.ready
    return 0
fi

if ! command -v nix-daemon >/dev/null 2>&1; then
    log "nix-daemon not found on PATH — skipping daemon spawn"
    notify nix.ready
    return 0
fi

# Always start fresh — the previous "skip if pgrep matches" optimization
# tripped on transient processes during entrypoint init (some helper that
# matches `nix-daemon` then exits), causing the fragment to think a daemon
# was running when none actually was. Robust path: kill stale daemon, rm
# stale socket, start new daemon, verify it stays alive.
log "Starting nix-daemon (logging to /tmp/nix-daemon.log)..."
# Kill any stale daemon left over from a previous container generation
# (e.g. cell restart with persistent /nix volume).
pkill -x nix-daemon 2>/dev/null
sleep 0.2
rm -f /nix/var/nix/daemon-socket/socket

# setsid → daemon survives the entrypoint's exec to gosu.
# Output captured to /tmp/nix-daemon.log so failures aren't silent —
# user can `tail /tmp/nix-daemon.log` to see why if the daemon dies.
# `touch` first so the log exists even when nix-daemon never writes to it
# (helps post-mortem debugging when daemon crashes before any output).
touch /tmp/nix-daemon.log
chmod 0644 /tmp/nix-daemon.log
setsid nix-daemon < /dev/null > /tmp/nix-daemon.log 2>&1 &
_daemon_pid=$!
disown

# Poll for the socket (usually <500ms; cap at 3s for the cold path).
for _i in $(seq 1 30); do
    [ -S /nix/var/nix/daemon-socket/socket ] && break
    sleep 0.1
done

# Verify the daemon is actually alive by checking its PID directly (not
# pgrep which matched transients earlier).
if [ -S /nix/var/nix/daemon-socket/socket ] && kill -0 "$_daemon_pid" 2>/dev/null; then
    log "nix-daemon ready (pid $_daemon_pid)"
    export NIX_REMOTE=daemon
else
    log "⚠ nix-daemon did not stay running. /tmp/nix-daemon.log tail:"
    tail -20 /tmp/nix-daemon.log 2>&1 | while IFS= read -r _line; do log "  $_line"; done
    # Don't export NIX_REMOTE — let the user's nix CLI try direct-mode and
    # fail loudly rather than connecting to a dead socket.
fi

notify nix.ready
