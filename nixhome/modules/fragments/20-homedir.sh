#!/bin/bash
# 20-homedir.sh — homedir setup (nix config, starship, repo homedir, browser env)
# Sourced by entrypoint.sh if present and executable.

# ── Nix-managed configs: NO COPY, use env vars ─────────────────────
# nix.conf, starship.toml, fontconfig, mise config are all nix-managed
# (symlinks → nix store in /opt/devcell/). NEVER copy them to $HOME —
# they'd become stale on the persistent bind mount after image rebuilds.
# Instead, env vars in shell rc overrides point tools at /opt/devcell/:
#   NIX_CONF_DIR, STARSHIP_CONFIG, FONTCONFIG_PATH (DIMM-24).
export FONTCONFIG_PATH="$DEVCELL_HOME/.config/fontconfig"

# ── Copy from repo's homedir/ (project-specific overrides) ──────────
if [ -d "$REPO_HOMEDIR" ]; then
    log "Syncing repo homedir to ~/ (ignore existing, exclude .claude)"
    rsync -a --copy-links --ignore-existing --exclude=.claude \
        --chown="$HOST_USER" "$REPO_HOMEDIR/" "$HOME/"
fi

# ── Browser environment ─────────────────────────────────────────────
export CHROMIUM_PROFILE_PATH="${HOME}/.chrome-${APP_NAME:-cell}"
export PLAYWRIGHT_MCP_USER_DATA_DIR="${HOME}/.playwright-${APP_NAME:-cell}"
