#!/bin/bash
# 10-mise.sh — mise runtime version manager setup
# Sourced by entrypoint.sh if present and executable.

command -v mise &>/dev/null || return 0

# ── Copy .tool-versions to session user home ─────────────────────────
# Written to /etc/devcell/tool-versions by nix activation (no dangling symlinks).
# Always overwrite — persistent $HOME may have a dangling symlink from a
# previous home-manager generation. cp refuses to write through dangling
# symlinks, so remove first (DIMM-24).
if [ -f /etc/devcell/tool-versions ]; then
    [ -L "$HOME/.tool-versions" ] && rm -f "$HOME/.tool-versions"
    cp /etc/devcell/tool-versions "$HOME/.tool-versions"
    chown "$HOST_USER" "$HOME/.tool-versions"
fi
# mise config and default-npm-packages are handled via env vars
# (MISE_GLOBAL_CONFIG_FILE, MISE_NODE_DEFAULT_PACKAGES_FILE) set in shell rc overrides.

# ── Setup ~/.local/share/mise (user-persisted MISE_DATA_DIR) ─────────
# /opt/mise holds baked-in installs (ephemeral, reset on each container start).
# ~/.local/share/mise (CellHome, bind-mounted) holds user-installed versions
# that persist. Baked-in versions are symlinked per-version so mise resolves
# both through a single MISE_DATA_DIR without copying data.
setup_mise_home() {
    local baked="/opt/mise"
    local user_mise="$HOME/.local/share/mise"
    local mise_bin="/opt/devcell/.local/state/nix/profiles/profile/bin/mise"

    mkdir -p "$user_mise/installs" "$user_mise/shims"

    # Symlink each baked-in tool version individually so user installs can coexist
    # as real directories alongside the symlinks.
    for tool_dir in "$baked/installs"/*/; do
        [ -d "$tool_dir" ] || continue
        tool_name=$(basename "$tool_dir")
        mkdir -p "$user_mise/installs/$tool_name"

        # Remove dangling symlinks left by superseded image versions.
        # Use * (not */) so dangling symlinks (no live target) are included.
        for link in "$user_mise/installs/$tool_name"/*; do
            if [ -L "$link" ] && [ ! -e "$link" ]; then rm -f "$link"; fi
        done

        # Symlink current baked-in versions (skip real dirs — user installs).
        for ver_dir in "$tool_dir"*/; do
            [ -d "$ver_dir" ] || continue
            ver_name=$(basename "$ver_dir")
            dest="$user_mise/installs/$tool_name/$ver_name"
            # Never overwrite a real directory (user-installed version).
            [ -d "$dest" ] && [ ! -L "$dest" ] && continue
            ln -sfT "$ver_dir" "$dest"
        done
    done

    chown -R "$HOST_USER" "$user_mise"

    # Regenerate shims for all currently visible installs.
    MISE_DATA_DIR="$user_mise" HOME="$HOME" "$mise_bin" reshim 2>/dev/null || true

    # Install any versions listed in ~/.tool-versions that aren't baked.
    # Skips if the file hasn't changed since the last successful install
    # (checksum stored in mise data dir). First start or edits trigger a full check.
    if [ -f "$HOME/.tool-versions" ]; then
        local tv_sha
        tv_sha=$(sha256sum "$HOME/.tool-versions" 2>/dev/null | cut -d' ' -f1)
        if [ -f "$user_mise/.tv-global.sha" ] && [ "$(cat "$user_mise/.tv-global.sha" 2>/dev/null)" = "$tv_sha" ]; then
            log "Global .tool-versions unchanged, skipping install"
        else
            log "Installing global tool versions from ~/.tool-versions..."
            (cd "$HOME" && MISE_DATA_DIR="$user_mise" HOME="$HOME" USER="$HOST_USER" \
                "$mise_bin" install -y 2>&1) | while IFS= read -r line; do log "$line"; done || true
            chown -R "$HOST_USER" "$user_mise"
            echo "$tv_sha" > "$user_mise/.tv-global.sha"
        fi
    fi

    # If the workspace has a .tool-versions, install any missing versions now so
    # they land in ~/.local/share/mise (CellHome) and persist — no re-download on next start.
    local workspace="/${APP_NAME:-}"
    if [ -n "$APP_NAME" ] && [ -f "$workspace/.tool-versions" ]; then
        local ws_sha
        ws_sha=$(sha256sum "$workspace/.tool-versions" 2>/dev/null | cut -d' ' -f1)
        if [ -f "$user_mise/.tv-workspace.sha" ] && [ "$(cat "$user_mise/.tv-workspace.sha" 2>/dev/null)" = "$ws_sha" ]; then
            log "Workspace .tool-versions unchanged, skipping install"
        else
            log "Installing workspace tool versions from $workspace/.tool-versions..."
            MISE_DATA_DIR="$user_mise" "$mise_bin" trust "$workspace/.tool-versions" 2>/dev/null || true
            (cd "$workspace" && MISE_DATA_DIR="$user_mise" HOME="$HOME" USER="$HOST_USER" \
                "$mise_bin" install -y 2>&1) | while IFS= read -r line; do log "$line"; done || true
            chown -R "$HOST_USER" "$user_mise"
            echo "$ws_sha" > "$user_mise/.tv-workspace.sha"
        fi
    fi
}
setup_mise_home

# ── Mise env exports ─────────────────────────────────────────────────
# Ensure mise env vars are correct for exec'd processes (e.g. claude)
# that don't source shell rc files and would otherwise inherit the container ENV
# which still points at the ephemeral /opt/mise.
export MISE_DATA_DIR="${HOME}/.local/share/mise"
export MISE_GLOBAL_CONFIG_FILE="$(readlink -f "$DEVCELL_HOME/.config/mise/config.toml" 2>/dev/null)"
export MISE_NODE_DEFAULT_PACKAGES_FILE="$(readlink -f "$DEVCELL_HOME/.default-npm-packages" 2>/dev/null)"
export PATH="${HOME}/.local/share/mise/shims:${PATH}"
