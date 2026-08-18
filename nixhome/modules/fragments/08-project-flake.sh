#!/bin/bash
# 08-project-flake.sh — install project-level flake.nix packages (CELL-447).
# Sourced by entrypoint.sh if present and executable.
#
# Detects flake.nix in the project root and installs its packages into
# a dedicated nix profile at $HOME/.local/state/nix/profiles/project.
# Trust is decided host-side by the cell CLI and passed via env var.
#
# Env vars:
#   DEVCELL_FLAKE_TRUST=1  — install project flake packages (set by cell CLI)
#   DEVCELL_SKIP_FLAKE=1   — degrade install failure to warning instead of boot abort

PROJECT_DIR="${WORKSPACE:-}"
[ -z "$PROJECT_DIR" ] && return 0
[ -f "$PROJECT_DIR/flake.nix" ] || return 0

if [ "${DEVCELL_FLAKE_TRUST:-}" != "1" ]; then
    log "project-flake: found $PROJECT_DIR/flake.nix but DEVCELL_FLAKE_TRUST not set — skipping"
    return 0
fi

# nix CLI needs experimental-features enabled; the config lives at the
# devcell home, not the session user's (shell rc hasn't sourced yet).
export NIX_CONF_DIR="${NIX_CONF_DIR:-/opt/devcell/.config/nix}"

FLAKE_CACHE="$HOME/.cache/devcell/project-flake"
FLAKE_PROFILE="$HOME/.local/state/nix/profiles/project"

mkdir -p "$FLAKE_CACHE"
chown -R "$HOST_USER" "$HOME/.cache/devcell" 2>/dev/null || true

# Compute hash of flake.nix + flake.lock (if present). Both matter:
# flake.lock tracks input revisions, flake.nix defines outputs.
_flake_hash=$(cat "$PROJECT_DIR/flake.nix" "$PROJECT_DIR/flake.lock" 2>/dev/null | sha256sum | cut -d' ' -f1)

notify project-flake.starting

# Hash gate: skip install if flake hasn't changed since last install.
_hash_file="$FLAKE_CACHE/flake.hash"
if [ -f "$_hash_file" ] && [ "$(cat "$_hash_file" 2>/dev/null)" = "$_flake_hash" ] && [ -d "$FLAKE_PROFILE" ]; then
    log "project-flake: unchanged, skipping install"
    export PATH="$FLAKE_PROFILE/bin${PATH:+:}$PATH"
    notify project-flake.ready
    return 0
fi

# libgit2 (used by nix flake eval) rejects bind-mounted repos not owned by
# the current user. Mark the project dir as safe for the session user.
gosu "$HOST_USER" git config --global --add safe.directory "$PROJECT_DIR" 2>/dev/null || true

log "project-flake: installing packages from $PROJECT_DIR/flake.nix..."

# Wipe the old profile before reinstalling so stale packages don't accumulate.
if [ -e "$FLAKE_PROFILE" ]; then
    rm -f "$FLAKE_PROFILE" "${FLAKE_PROFILE}"-*
    log "project-flake: cleared previous profile"
fi

# Determine system for flake output path.
case "$(uname -m)" in
    aarch64) _system="aarch64-linux" ;;
    *)       _system="x86_64-linux" ;;
esac

# Try packages output first, then devShells.inputDerivation (mkShell can't
# be installed directly: its buildPhase exits 1 by design).
_install_log=$(mktemp)
_installed=false

if gosu "$HOST_USER" nix profile install \
    --profile "$FLAKE_PROFILE" \
    "$PROJECT_DIR#packages.$_system.default" > "$_install_log" 2>&1; then
    _installed=true
    log "project-flake: installed packages.$_system.default"
else
    log "project-flake: no packages.$_system.default, trying devShell inputDerivation..."
    if gosu "$HOST_USER" nix profile install \
        --profile "$FLAKE_PROFILE" \
        "$PROJECT_DIR#devShells.$_system.default.inputDerivation" >> "$_install_log" 2>&1; then
        _installed=true
        log "project-flake: installed devShells.$_system.default.inputDerivation"
    fi
fi

# Log output from install (success or failure).
while IFS= read -r line; do log "  $line"; done < "$_install_log"
rm -f "$_install_log"

if [ "$_installed" = true ]; then
    echo "$_flake_hash" > "$_hash_file"
    chown -R "$HOST_USER" "$FLAKE_CACHE" 2>/dev/null || true
    chown -R "$HOST_USER" "$HOME/.local/state/nix/profiles" 2>/dev/null || true

    # Stamp GC root so nix gc doesn't collect the project profile's store paths.
    _proj_store=$(readlink -f "$FLAKE_PROFILE" 2>/dev/null)
    if [ -n "$_proj_store" ] && [ -d /nix/var/nix/gcroots/devcell ]; then
        _proj_hash=$(basename "$_proj_store" | cut -d- -f1)
        ln -sfT "$_proj_store" "/nix/var/nix/gcroots/devcell/${_proj_hash}-project-profile"
        log "project-flake: GC root stamped"
    fi

    export PATH="$FLAKE_PROFILE/bin${PATH:+:}$PATH"
    log "project-flake: installed successfully"
else
    log "project-flake: install failed — tried packages.$_system.default and devShells.$_system.default.inputDerivation"
    log "project-flake: check that your flake.nix exposes one of these outputs"
    if [ "${DEVCELL_SKIP_FLAKE:-}" = "1" ]; then
        log "project-flake: DEVCELL_SKIP_FLAKE=1 — continuing despite failure"
    else
        echo "" >&2
        echo "ERROR: project-flake install failed." >&2
        echo "  To start without project flake packages: --skip-flake or DEVCELL_SKIP_FLAKE=1" >&2
        echo "" >&2
        notify project-flake.ready
        exit 1
    fi
fi

notify project-flake.ready
