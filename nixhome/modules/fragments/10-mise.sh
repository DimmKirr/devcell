#!/bin/bash
# 10-mise.sh — mise runtime version manager setup
# Sourced by entrypoint.sh if present and executable.

command -v mise &>/dev/null || return 0

notify mise.starting

# ── Copy .tool-versions to session user home ─────────────────────────
# Written to /etc/devcell/tool-versions by nix activation (no dangling symlinks).
# Always overwrite — persistent $HOME may have a dangling symlink from a
# previous home-manager generation. cp refuses to write through dangling
# symlinks, so remove first.
if [ -f /etc/devcell/tool-versions ]; then
    [ -L "$HOME/.tool-versions" ] && rm -f "$HOME/.tool-versions"
    cp /etc/devcell/tool-versions "$HOME/.tool-versions"
    chown "$HOST_USER" "$HOME/.tool-versions"
fi
# mise config and default-npm-packages are handled via env vars
# (MISE_GLOBAL_CONFIG_FILE, MISE_NODE_DEFAULT_PACKAGES_FILE) set in shell rc overrides.

# ── Setup ~/.local/share/mise (user-persisted MISE_DATA_DIR) ─────────
# Baked installs are resolved natively by mise via MISE_SHARED_INSTALL_DIRS
# (read-only, set as image env; mise ≥2026.3.9). ~/.local/share/mise
# (CellHome, bind-mounted) holds user-installed versions, which take
# precedence — like PATH, but for mise installs. No symlinks or copies of
# baked tools ever land in $HOME (the old cross-bind design dangled on
# every image rebuild, CELL-75).
setup_mise_home() {
    local user_mise="$HOME/.local/share/mise"
    local mise_bin
    mise_bin="$(command -v mise 2>/dev/null)" || mise_bin="/opt/devcell/.local/state/nix/profiles/profile/bin/mise"

    mkdir -p "$user_mise/installs" "$user_mise/shims"

    # ── Clean cross-tier install symlinks UNCONDITIONALLY (CELL-85/294) ─
    # Older images cross-bound baked installs into $HOME as ABSOLUTE
    # symlinks (→ /opt/...); those dangle whenever the baked location moves
    # or versions change between image generations, and dangling links trick
    # mise reshim into skipping the tool ("install not found"): the historic
    # terraform/opentofu missing-shims bug. Remove absolute-target links and
    # dangling links — but PRESERVE mise's own relative version aliases
    # (24 -> ./24.16.0, latest -> ...): deleting those forces mise to
    # recreate them and invalidates the sha-gate on every boot.
    local cleanup_removed=0
    for tool_dir in "$user_mise/installs"/*; do
        [ -d "$tool_dir" ] || continue
        for link in "$tool_dir"/*; do
            [ -L "$link" ] || continue
            case "$(readlink "$link")" in
                /*) ;;                          # absolute → legacy cross-bind
                *) [ -e "$link" ] && continue ;; # relative + resolves → mise alias, keep
            esac
            log "Removing legacy/stale mise install symlink: $link"
            rm -f "$link"
            cleanup_removed=1
        done
    done

    # ── Detect stale installs (empty tool dir) so sha-gate can't skip ────
    # Covers the manual-wipe case: `rm -rf installs/terraform/*` leaves an
    # empty tool dir with no symlinks for the cleanup loop above to detect.
    # If ANY tool dir under installs/ is empty, treat the state as stale and
    # let the sha invalidation below force a reinstall.
    for tool_dir in "$user_mise/installs"/*; do
        [ -d "$tool_dir" ] || continue
        if [ -z "$(ls -A "$tool_dir" 2>/dev/null)" ]; then
            log "Tool install dir empty ($(basename "$tool_dir")) — marking state stale"
            cleanup_removed=1
            break
        fi
    done

    # ── Invalidate sha-gate when cleanup wiped any install (CELL-66) ────
    # The install steps below skip `mise install -y` when ~/.tool-versions's
    # sha matches the value persisted at .tv-{global,workspace}.sha. But if
    # cleanup just removed install symlinks (or any tool dir is empty), the
    # declared tools are gone from disk while the sha still matches →
    # install skipped → tools stay (missing) on PATH until the user manually
    # wipes the sha file. Drop both shas so install runs unconditionally.
    if [ "$cleanup_removed" = 1 ]; then
        log "Stale mise state detected — invalidating .tv-*.sha to force reinstall"
        rm -f "$user_mise/.tv-global.sha" "$user_mise/.tv-workspace.sha"
    fi

    # Install any versions listed in ~/.tool-versions that aren't baked.
    # Skips if the file hasn't changed since the last successful install
    # (checksum stored in mise data dir). First start or edits trigger a full check.
    #
    # The install/reshim/chown trio sits behind a single sha-gate: on warm
    # boots where ~/.tool-versions hasn't changed since the last run, every
    # recursive walk of $user_mise (~17k entries on the persistent bind
    # mount) is skipped. Cold/changed boots pay the full cost once and
    # update .tv-global.sha so subsequent boots skip again. The cleanup
    # loops above invalidate .tv-global.sha when state is stale, so the
    # else branch reliably re-runs when work is actually needed.
    if [ -f "$HOME/.tool-versions" ]; then
        local tv_sha
        tv_sha=$(sha256sum "$HOME/.tool-versions" 2>/dev/null | cut -d' ' -f1)
        if [ -f "$user_mise/.tv-global.sha" ] && [ "$(cat "$user_mise/.tv-global.sha" 2>/dev/null)" = "$tv_sha" ]; then
            log "Global .tool-versions unchanged, skipping install/reshim/chown"
        else
            log "Installing global tool versions from ~/.tool-versions..."
            (cd "$HOME" && MISE_DATA_DIR="$user_mise" HOME="$HOME" USER="$HOST_USER" \
                "$mise_bin" install -y 2>&1) | while IFS= read -r line; do log "$line"; done || true
            chown -R "$HOST_USER" "$user_mise"

            # Regenerate shims for all currently visible installs — including
            # shared (baked) ones, which mise discovers via
            # MISE_SHARED_INSTALL_DIRS. Failures are logged loudly — silenced
            # failures historically hid the terraform/opentofu missing-shim bug.
            if ! MISE_DATA_DIR="$user_mise" HOME="$HOME" "$mise_bin" reshim 2>&1; then
                log "⚠ mise reshim failed — tool shims may be missing from PATH"
            fi

            # Fix ownership of mise state dir (created by reshim running as root).
            [ -d "$HOME/.local/state/mise" ] && chown -R "$HOST_USER" "$HOME/.local/state/mise"
            # Cache too: install/reshim write lockfiles + metadata to
            # ~/.cache/mise as root even when versions resolve from the shared
            # layer; left root-owned, user-level `mise install` (project
            # .tool-versions pins) fails with EACCES on the lockfile dir.
            [ -d "$HOME/.cache/mise" ] && chown -R "$HOST_USER" "$HOME/.cache/mise"

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
            [ -d "$HOME/.cache/mise" ] && chown -R "$HOST_USER" "$HOME/.cache/mise"
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

notify mise.ready
