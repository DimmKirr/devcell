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
#     - setuid sudo wrapper       — nix store paths are 0555 and /nix is shared
#                                    across containers, so the store can never
#                                    hold setuid. Copy sudo to /run/wrappers/bin
#                                    and setuid the copy, else `sudo` errors
#                                    "must be owned by uid 0 and have the setuid
#                                    bit set" and sudo-from-cell is broken.
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

# ── ALWAYS: setuid sudo wrapper (CELL-358) ──────────────────────────────
# Nix store paths are 0555 and /nix is a volume SHARED by every container,
# so the store can never carry the setuid bit. The old fix (chmod u+s on the
# store path) was wrong on a shared volume: any `nix profile upgrade` moves
# the profile symlink to a fresh 0555 path, breaking sudo in EVERY container
# at once, and one container's chmod leaked a setuid binary to all others.
#
# Instead use the NixOS security-wrappers pattern: copy sudo to a
# container-local dir on the overlay and set setuid on the copy. The copy is
# immune to profile rebuilds (scenario: rebuild in cell A leaves cell B's
# sudo untouched), mutates nothing on the shared volume, and is refreshed
# from the current profile on every container start.
log "Installing setuid sudo wrapper..."
_sudo_store=$(readlink -f "$(command -v sudo 2>/dev/null)" 2>/dev/null)
if [ -n "$_sudo_store" ] && [ -e "$_sudo_store" ]; then
    if install -D -m 4755 -o root -g root "$_sudo_store" /run/wrappers/bin/sudo 2>/dev/null; then
        log "  wrapper installed from $_sudo_store"

        # Pin the closure. The copy dlopens sudoers.so / libsudo_util.so from
        # its store path at runtime, and once a profile upgrade moves on,
        # nothing else roots the old closure — a GC would rip the plugins out
        # from under the running copy. Named per store hash so containers on
        # different sudo versions each keep their own pin instead of
        # clobbering a shared name; the -wrapper- infix keeps it clear of the
        # *-profile / *-meta globs that the GC reaper walks.
        _sudo_pkg="${_sudo_store%/bin/sudo}"
        _sudo_hash=$(basename "$_sudo_pkg" | cut -d- -f1)
        mkdir -p /nix/var/nix/gcroots/devcell 2>/dev/null
        ln -sfT "$_sudo_pkg" "/nix/var/nix/gcroots/devcell/sudo-wrapper-${_sudo_hash}" 2>/dev/null \
            && log "  pinned closure via gcroots/devcell/sudo-wrapper-${_sudo_hash}"

        # Repoint the FHS paths scripts hardcode. Also unifies pure and thin:
        # pure images ship /bin/sudo from systemTools, thin images ship none.
        for _p in /bin/sudo /usr/bin/sudo /sbin/sudo; do
            ln -sfT /run/wrappers/bin/sudo "$_p" 2>/dev/null || true
        done
    else
        log "  ⚠ could not install sudo wrapper into /run/wrappers/bin"
    fi
else
    log "  ⚠ sudo not found on PATH — skipping wrapper install"
fi

# PAM stub. Thin images have no /etc/pam.d at all, so sudo aborts with
# "PAM account management error" even once setuid is correct. Pure images
# bake this via image.nix extraDirs (CELL-86); only create it when missing.
# Bare module name resolves through libpam's own store lib/security dir.
if [ ! -f /etc/pam.d/sudo ]; then
    mkdir -p /etc/pam.d
    printf 'auth     sufficient pam_permit.so\naccount  sufficient pam_permit.so\nsession  sufficient pam_permit.so\npassword sufficient pam_permit.so\n' > /etc/pam.d/sudo
    chmod 0644 /etc/pam.d/sudo
    log "  wrote /etc/pam.d/sudo stub"
fi

# Self-test. setuid is silently neutralized by a nosuid mount on /run or by
# --security-opt no-new-privileges; both install cleanly and then fail at
# first use, so surface it here rather than letting users discover it later.
if [ ! -u /run/wrappers/bin/sudo ] 2>/dev/null; then
    log "  ⚠ sudo wrapper has no setuid bit — sudo will not work"
elif ! grep -q '^NoNewPrivs:[[:space:]]*0' /proc/self/status 2>/dev/null; then
    log "  ⚠ no-new-privileges is set — setuid is neutralized, sudo will not work"
fi

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
