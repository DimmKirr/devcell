#!/bin/bash
# 05-shell-rc.sh — shell rc files + nix profile for session user
# Sourced by entrypoint.sh if present and executable.

notify shell.starting

# ── Nix profile symlink + user-writable profile dir ──────────────────
# Goal: `nix profile add nixpkgs#htop` works for the session user out of
# the box, AND the baked home-manager profile (at /opt/devcell/...)
# stays on PATH as a fallback.
#
# Pre-2026-05-15 setup pointed $HOME/.nix-profile at the read-only
# /opt/devcell/.nix-profile — any `nix profile add` then tripped because
# nix tried to create a new generation alongside the baked profile in
# /opt/devcell, which the session user (uid 1001+) can't write to.
#
# New setup:
#   1. Ensure $HOME/.local/state/nix/profiles/ exists, owned by HOST_USER.
#   2. Remove any stale $HOME/.nix-profile symlink whose target escapes
#      $HOME (likely points at /opt/devcell/... from prior containers
#      because $HOME is bind-mounted from the Mac and survives rebuilds).
#   3. Recreate $HOME/.nix-profile → $HOME/.local/state/nix/profiles/profile.
#      nix's default profile resolution lands inside $HOME and writes succeed.
#   PATH (further down) puts the user profile FIRST and the baked profile
#   SECOND, so user installs override stack defaults but stack tools
#   remain reachable.
mkdir -p "$HOME/.local/state/nix/profiles"
chown -R "$HOST_USER" "$HOME/.local/state/nix" 2>/dev/null || true
# Drop the stale symlink (target outside $HOME) so the fresh ln below
# isn't trying to follow it.
if [ -L "$HOME/.nix-profile" ]; then
    _tgt=$(readlink "$HOME/.nix-profile")
    case "$_tgt" in
        "$HOME"/*) : ;;  # already inside $HOME, keep it
        *) rm -f "$HOME/.nix-profile" ;;
    esac
fi
ln -sfT "$HOME/.local/state/nix/profiles/profile" "$HOME/.nix-profile"
chown -h "$HOST_USER" "$HOME/.nix-profile" 2>/dev/null || true

# ── Shell rc files — source devcell rc + apply session-user overrides ──
# home-manager only generates rc files for shells it manages. We enable
# programs.zsh in nixhome (so /opt/devcell/.zshrc + .zshenv exist) but NOT
# programs.bash — so /opt/devcell/.bashrc and .profile DO NOT exist on the
# pure or impure images.
#
# Pre-fix: this loop skipped .bashrc and .profile (continue on missing
# DEVCELL_HOME file), leaving the session user with NO ~/.profile. Any
# `bash -lc` (CI tests, IDE exec hooks, mise activation, sudo -i) then
# read /etc/profile only and got system-default PATH with NO mise shim
# dir and NO nix profile bin — declared mise tools (go, terraform, node,
# opentofu) silently absent on PATH despite working shims under
# $HOME/.local/share/mise/shims.
#
# Fix: write override content UNCONDITIONALLY for every rc file the session
# user can hit. Source DEVCELL_HOME's version only when it exists (zsh case);
# bash-side files (.profile, .bashrc) get pure-override content.
overrides() {
    # Optional sourceCmd as $1 — line to source devcell rc, empty for bash files.
    local sourceCmd="$1"
    [ -n "$sourceCmd" ] && echo "$sourceCmd"
    cat <<RCEOF
# -- devcell session user overrides --
export USER="$HOST_USER"
export GOPATH="$HOME/go"
export MISE_DATA_DIR="$HOME/.local/share/mise"
export MISE_GLOBAL_CONFIG_FILE="$(readlink -f "$DEVCELL_HOME/.config/mise/config.toml" 2>/dev/null)"
export MISE_NODE_DEFAULT_PACKAGES_FILE="$(readlink -f "$DEVCELL_HOME/.default-npm-packages" 2>/dev/null)"
export NIX_CONF_DIR="/opt/devcell/.config/nix"
export STARSHIP_CONFIG="/opt/devcell/.config/starship.toml"
export FONTCONFIG_PATH="/opt/devcell/.config/fontconfig"
export HISTFILE="$HOME/.zsh_history"
export PATH="$HOME/go/bin:$HOME/.local/state/nix/profiles/profile/bin:/opt/devcell/.local/state/nix/profiles/profile/bin:$HOME/.local/share/mise/shims\${PATH:+:}\${PATH}"
# NIX_LD_LIBRARY_PATH bootstrap for shells — covers docker exec / IDE attach
# that bypass entrypoint.d/06-nix-ldpath.sh. Points at the merged .nix-ld-libs/
# directory (symlinks to every .so* from the profile closure, glibc excluded).
_NLD="/opt/devcell/.nix-ld-libs"
if [ -d "\$_NLD" ] && [ -z "\${_DEVCELL_NIX_LD_LIBPATH_SET:-}" ]; then
  export NIX_LD_LIBRARY_PATH="\$_NLD\${NIX_LD_LIBRARY_PATH:+:}\${NIX_LD_LIBRARY_PATH}"
  export _DEVCELL_NIX_LD_LIBPATH_SET=1
fi
RCEOF
}

# zsh files: source devcell rc if present (home-manager-managed), then overrides.
for file in .zshrc .zshenv; do
    if [ -f "$DEVCELL_HOME/$file" ]; then
        overrides ". \"$DEVCELL_HOME/$file\"" > "$HOME/$file"
    else
        overrides "" > "$HOME/$file"
    fi
    chown "$HOST_USER" "$HOME/$file"
done

# bash/sh login files: always create. /opt/devcell/.profile and .bashrc
# don't exist (programs.bash not enabled in home-manager), so we don't try
# to source them — overrides are self-contained. `bash -lc` reads .profile;
# non-login interactive bash reads .bashrc.
for file in .profile .bashrc; do
    if [ -f "$DEVCELL_HOME/$file" ]; then
        overrides ". \"$DEVCELL_HOME/$file\"" > "$HOME/$file"
    else
        overrides "" > "$HOME/$file"
    fi
    chown "$HOST_USER" "$HOME/$file"
done

notify shell.ready
