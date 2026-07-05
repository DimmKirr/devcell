#!/bin/bash
# 20-homedir.sh — homedir setup (nix config, starship, repo homedir, browser env)
# Sourced by entrypoint.sh if present and executable.

notify home.starting

# ── Nix-managed configs: NO COPY, use env vars ─────────────────────
# nix.conf, starship.toml, fontconfig, mise config are all nix-managed
# (symlinks → nix store in /opt/devcell/). NEVER copy them to $HOME —
# they'd become stale on the persistent bind mount after image rebuilds.
# Instead, env vars in shell rc overrides point tools at /opt/devcell/:
#   NIX_CONF_DIR, STARSHIP_CONFIG, FONTCONFIG_PATH.
export FONTCONFIG_PATH="$DEVCELL_HOME/.config/fontconfig"

# ── Copy from repo's homedir/ (project-specific overrides) ──────────
if [ -d "$REPO_HOMEDIR" ]; then
    log "Syncing repo homedir to ~/ (ignore existing, exclude .claude)"
    rsync -a --copy-links --ignore-existing --exclude=.claude \
        --chown="$HOST_USER" "$REPO_HOMEDIR/" "$HOME/"
fi

# ── Browser environment (CELL-74 layout) ───────────────────────────
# Both interactive chromium (over RDP/VNC) and patchright-mcp-cell path ③
# fallback point at the same per-app Chromium user-data-dir. Sharing the
# profile is safe because (a) wrapper path ① detects a running chromium
# via CDP port 9222 and attaches instead of launching a second instance,
# and (b) 22-chromium-singleton.sh clears stale SingletonLock files on
# every container start.
#
# In-container path:  $HOME/.chrome/<app>/
# Host path (Mac):    $HOME/.devcell/<session>/.chrome/<app>/   (same file via bind mount)
export CHROMIUM_PROFILE_PATH="${HOME}/.chrome/${APP_NAME:-cell}"
export PLAYWRIGHT_MCP_USER_DATA_DIR="${HOME}/.chrome/${APP_NAME:-cell}"

notify home.ready
