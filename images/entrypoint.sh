#!/bin/bash
# Entrypoint script — runs as root, drops to HOST_USER via gosu at the end.
#
# /opt/devcell  — nix environment home (owned by devcell, read-only for session user)
# /home/$HOST_USER — session user's personal home (writable)

DEVCELL_HOME="/opt/devcell"
REPO_HOMEDIR="${WORKSPACE}/homedir"
HOST_USER="${HOST_USER:-devcell}"
export USER="$HOST_USER"
export HOME="/home/$HOST_USER"

# ── Verbose logging — only active when DEVCELL_DEBUG=true ─────────────────────
# Default is silent so containers launched without the flag stay quiet.
if [ "${DEVCELL_DEBUG:-false}" = "true" ]; then
    log() { echo "$@"; }
else
    log() { :; }
fi

# ── Create session user if needed ─────────────────────────────────────────────
if ! id "$HOST_USER" &>/dev/null; then
    useradd -m -s /bin/zsh "$HOST_USER" 2>/dev/null
    echo "$HOST_USER ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
fi

# ── Grant docker socket access to session user ────────────────────────────────
# The socket GID varies per host; resolve it at runtime and add HOST_USER to
# whatever group owns the socket (creating it if it doesn't exist inside the
# container yet).
if [ -S /var/run/docker.sock ]; then
    DOCKER_GID=$(stat -c '%g' /var/run/docker.sock)
    DOCKER_GROUP=$(getent group "$DOCKER_GID" | cut -d: -f1)
    if [ -z "$DOCKER_GROUP" ]; then
        groupadd -g "$DOCKER_GID" dockerhost
        DOCKER_GROUP=dockerhost
    fi
    usermod -aG "$DOCKER_GROUP" "$HOST_USER"
fi

mkdir -p "$HOME/.local/bin" "$HOME/tmp"
chown "$HOST_USER" "$HOME/.local/bin" "$HOME/tmp"

# ── Give session user access to devcell's nix environment ─────────────────────
# nix.sh resolves $HOME/.nix-profile to find nix tools. Create a symlink so
# the session user's login shell gets nix in PATH without running home-manager.
ln -sfT "$(readlink -f /opt/devcell/.nix-profile)" "$HOME/.nix-profile"

# ── Copy shell init files, override write-path vars for session user ──────────
# .profile sources nix.sh from the literal /opt/devcell path — copy as-is so
# nix stays in PATH. hm-session-vars.sh hardcodes GOPATH=/opt/devcell/go, so
# we append explicit overrides pointing write paths at session $HOME.
for file in .bashrc .zshrc .profile; do
    if [ -f "$DEVCELL_HOME/$file" ]; then
        cp "$DEVCELL_HOME/$file" "$HOME/$file"
        # Override write-path vars set by hm-session-vars.sh, and session identity.
        # Explicitly include nix profile bin via compat symlink (world-readable);
        # /opt/devcell/ is 700 so testuser cannot follow the .nix-profile symlink there.
        printf '\n# -- devcell session user overrides --\nexport USER="%s"\nexport GOPATH="%s/go"\nexport ASDF_DATA_DIR="%s/.asdf"\nexport PATH="%s/go/bin:/nix/var/nix/profiles/per-user/devcell/profile/bin:%s/.asdf/shims:/opt/python-tools/.venv/bin:/opt/npm-tools/node_modules/.bin${PATH:+:}${PATH}"\n' \
            "$HOST_USER" "$HOME" "$HOME" "$HOME" "$HOME" >> "$HOME/$file"
        chown "$HOST_USER" "$HOME/$file"
    fi
done

for file in .asdfrc .tool-versions; do
    [ -e "$DEVCELL_HOME/$file" ] && [ ! -e "$HOME/$file" ] \
        && cp -r "$DEVCELL_HOME/$file" "$HOME/$file" && chown -R "$HOST_USER" "$HOME/$file"
done

# ── Setup ~/.asdf (user-persisted ASDF_DATA_DIR) ──────────────────────────────
# /opt/asdf holds baked-in installs (ephemeral, reset on each container start).
# ~/.asdf (CellHome, bind-mounted) holds user-installed versions that persist.
# Baked-in versions are symlinked per-version into ~/.asdf so asdf resolves both
# through a single ASDF_DATA_DIR without copying data.
setup_asdf_home() {
    local baked="/opt/asdf"
    local user_asdf="$HOME/.asdf"
    local asdf_bin="/nix/var/nix/profiles/per-user/devcell/profile/bin/asdf"

    mkdir -p "$user_asdf/installs" "$user_asdf/plugins" "$user_asdf/shims"

    # Symlink each baked-in tool version individually so user installs can coexist
    # as real directories alongside the symlinks.
    for tool_dir in "$baked/installs"/*/; do
        [ -d "$tool_dir" ] || continue
        tool_name=$(basename "$tool_dir")
        mkdir -p "$user_asdf/installs/$tool_name"

        # Remove dangling symlinks left by superseded image versions.
        # Use * (not */) so dangling symlinks (no live target) are included.
        for link in "$user_asdf/installs/$tool_name"/*; do
            if [ -L "$link" ] && [ ! -e "$link" ]; then rm -f "$link"; fi
        done

        # Symlink current baked-in versions (skip real dirs — user installs).
        for ver_dir in "$tool_dir"*/; do
            [ -d "$ver_dir" ] || continue
            ver_name=$(basename "$ver_dir")
            dest="$user_asdf/installs/$tool_name/$ver_name"
            # Never overwrite a real directory (user-installed version).
            [ -d "$dest" ] && [ ! -L "$dest" ] && continue
            ln -sfT "$ver_dir" "$dest"
        done
    done

    # Symlink baked-in plugins (only if not already a real/user-managed dir).
    for plugin_dir in "$baked/plugins"/*/; do
        [ -d "$plugin_dir" ] || continue
        plugin_name=$(basename "$plugin_dir")
        dest="$user_asdf/plugins/$plugin_name"
        [ -d "$dest" ] && [ ! -L "$dest" ] && continue
        ln -sfT "$plugin_dir" "$dest"
    done

    chown -R "$HOST_USER" "$user_asdf"

    # Regenerate shims into ~/.asdf/shims for all currently visible installs.
    ASDF_DATA_DIR="$user_asdf" HOME="$HOME" "$asdf_bin" reshim 2>/dev/null || true

    # Install any versions listed in ~/.tool-versions that aren't baked.
    # Runs on first start (or after user edits the file); subsequent starts are
    # instant because installed versions persist in the cell home.
    if [ -f "$HOME/.tool-versions" ]; then
        log "Installing global tool versions from ~/.tool-versions..."
        (cd "$HOME" && ASDF_DATA_DIR="$user_asdf" HOME="$HOME" USER="$HOST_USER" \
            "$asdf_bin" install 2>&1) | while IFS= read -r line; do log "$line"; done || true
        chown -R "$HOST_USER" "$user_asdf"
    fi

    # If the workspace has a .tool-versions, install any missing versions now so
    # they land in ~/.asdf (CellHome) and persist — no re-download on next start.
    local workspace="/${APP_NAME:-}"
    if [ -n "$APP_NAME" ] && [ -f "$workspace/.tool-versions" ]; then
        log "Installing workspace tool versions from $workspace/.tool-versions..."
        (cd "$workspace" && ASDF_DATA_DIR="$user_asdf" HOME="$HOME" USER="$HOST_USER" \
            "$asdf_bin" install 2>&1) | while IFS= read -r line; do log "$line"; done || true
        chown -R "$HOST_USER" "$user_asdf"
    fi
}
setup_asdf_home

if [ -d "$DEVCELL_HOME/.config/nix" ] && [ ! -d "$HOME/.config/nix" ]; then
    mkdir -p "$HOME/.config"
    cp -r "$DEVCELL_HOME/.config/nix" "$HOME/.config/"
    chown -R "$HOST_USER" "$HOME/.config/nix"
fi

# ── Copy from repo's homedir/ (project-specific overrides) ───────────────────
merge_claude_settings() {
    local template_file="$1" target_file="$2"
    [ -f "$template_file" ] || return 1
    mkdir -p "$(dirname "$target_file")"
    if [ ! -f "$target_file" ]; then
        log "Creating Claude settings from template"
        cp "$template_file" "$target_file"
        return 0
    fi
    local backup_file="${target_file}.backup-$(date +%Y%m%d-%H%M%S)"
    cp "$target_file" "$backup_file"
    log "✓ Created backup: $(basename "$backup_file")"
    ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true
    log "Merging Claude settings (preserving existing configuration)"
    local temp_file=$(mktemp)
    jq -s '
      if .[0].hooks.PermissionRequest then .[0]
      else .[0] * .[1]
      end
    ' "$target_file" "$template_file" > "$temp_file" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        grep -q "PermissionRequest" "$target_file" \
            && log "✓ Claude settings updated (PermissionRequest hook configured)" \
            || log "✓ Claude settings preserved (custom PermissionRequest hook detected)"
    else
        echo "⚠ Failed to merge Claude settings, restoring from backup"
        cp "$backup_file" "$target_file"
        rm -f "$temp_file"
    fi
}

merge_claude_nix() {
    local nix_hooks_dir="/etc/claude-code/hooks"
    local nix_settings="/etc/claude-code/nix-settings.json"
    if [ -d "$nix_hooks_dir" ] && [ -n "$(ls -A "$nix_hooks_dir" 2>/dev/null)" ]; then
        mkdir -p "$HOME/.claude/hooks"
        cp -r "$nix_hooks_dir/"* "$HOME/.claude/hooks/" 2>/dev/null || true
        find "$HOME/.claude/hooks" -type f -name "*.sh" -exec chmod +x {} \; 2>/dev/null || true
        log "✓ Claude hooks installed from nix"
    fi
    if [ -f "$nix_settings" ]; then
        merge_claude_settings "$nix_settings" "$HOME/.claude/settings.json"
    fi
}

merge_claude_mcp() {
    local target_file="$1"
    local nix_file="/etc/claude-code/nix-mcp-servers.json"

    # No nix MCP servers configured — nothing to do.
    [ -f "$nix_file" ] || return 0

    # Validate nix source file before touching anything.
    if ! jq empty "$nix_file" 2>/dev/null; then
        echo "⚠ nix-mcp-servers.json is invalid JSON — skipping MCP merge"
        return 1
    fi

    local backup_before_merge
    backup_before_merge=$(jq -r '.backupBeforeMerge // true' "$nix_file")

    mkdir -p "$(dirname "$target_file")"

    # Fresh start — no existing ~/.claude.json.
    if [ ! -f "$target_file" ]; then
        log "Creating ~/.claude.json with nix MCP servers"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcpServers: (.mcpServers // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/.claude.json created ($(jq '.mcpServers | length' "$target_file") server(s))"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to create ~/.claude.json from nix MCP servers"
            return 1
        fi
        return 0
    fi

    # Existing file is corrupt — back it up and recreate rather than abort.
    if ! jq empty "$target_file" 2>/dev/null; then
        local corrupt_bak="${target_file}.corrupt-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$corrupt_bak"
        log "⚠ ~/.claude.json was corrupt — saved to $(basename "$corrupt_bak"), recreating"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcpServers: (.mcpServers // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/.claude.json recreated"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to recreate ~/.claude.json"
            return 1
        fi
        return 0
    fi

    # Optional pre-merge backup.
    local backup_file=""
    if [ "$backup_before_merge" = "true" ]; then
        backup_file="${target_file}.backup-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$backup_file"
        log "✓ Created backup: $(basename "$backup_file")"
        # Keep only 5 most recent backups.
        ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true
    fi

    # Merge: nix servers are written over same-named user entries (infra wins).
    # User servers with unique names are preserved unchanged.
    local temp_file
    temp_file=$(mktemp)
    jq -s '
      .[0] as $existing |
      .[1].mcpServers as $nix |
      $existing | .mcpServers = (($existing.mcpServers // {}) + ($nix // {}))
    ' "$target_file" "$nix_file" > "$temp_file" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        log "✓ MCP servers merged into ~/.claude.json ($(jq '.mcpServers | length' "$target_file") total)"
    else
        rm -f "$temp_file"
        echo "⚠ Failed to merge MCP servers — keeping original"
        if [ -n "$backup_file" ] && [ -f "$backup_file" ]; then
            cp "$backup_file" "$target_file"
            echo "✓ Restored from backup"
        fi
        return 1
    fi
}

merge_claude_nix

if [ -d "$REPO_HOMEDIR" ]; then
    log "Found repo homedir at $REPO_HOMEDIR"
    find "$REPO_HOMEDIR" -mindepth 1 -maxdepth 1 | while read -r item; do
        basename_item=$(basename "$item")
        [ "$basename_item" = ".claude" ] && continue
        dest="$HOME/$basename_item"
        if [ ! -e "$dest" ]; then
            log "Copying $basename_item from repo to ~/"
            cp -r "$item" "$dest"
        fi
    done
fi

merge_opencode_providers() {
    local target_file="$1"
    local nix_file="/etc/opencode/nix-providers.json"

    [ -f "$nix_file" ] || return 0

    if ! jq empty "$nix_file" 2>/dev/null; then
        echo "⚠ nix-providers.json (OpenCode) is invalid JSON — skipping provider merge"
        return 1
    fi

    mkdir -p "$(dirname "$target_file")"

    if [ ! -f "$target_file" ]; then
        # No user config yet — seed it from nix providers
        local temp_file
        temp_file=$(mktemp)
        jq '{"$schema":"https://opencode.ai/config.json","provider": .provider}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/opencode.json created with $(jq '.provider | length' "$target_file") nix provider(s)"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to create ~/opencode.json"
        fi
        return 0
    fi

    if ! jq empty "$target_file" 2>/dev/null; then
        echo "⚠ ~/opencode.json is invalid JSON — skipping provider merge"
        return 1
    fi

    # Merge: inject nix providers only where the key is absent in user config
    local temp_file
    temp_file=$(mktemp)
    jq -s '
      .[0] as $existing |
      .[1].provider as $nix |
      $existing | .provider = (($nix // {}) + ($existing.provider // {}))
    ' "$target_file" "$nix_file" > "$temp_file" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        log "✓ OpenCode providers merged into ~/opencode.json"
    else
        rm -f "$temp_file"
        echo "⚠ Failed to merge OpenCode providers — keeping original"
    fi
}

merge_opencode_mcp() {
    local target_file="$1"
    local nix_file="/etc/opencode/nix-mcp-servers.json"

    [ -f "$nix_file" ] || return 0

    if ! jq empty "$nix_file" 2>/dev/null; then
        echo "⚠ nix-mcp-servers.json (OpenCode) is invalid JSON — skipping MCP merge"
        return 1
    fi

    local backup_before_merge
    backup_before_merge=$(jq -r '.backupBeforeMerge // true' "$nix_file")

    mkdir -p "$(dirname "$target_file")"

    if [ ! -f "$target_file" ]; then
        log "Creating ~/.opencode.json with nix MCP servers"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcp: (.mcp // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/.opencode.json created ($(jq '.mcp | length' "$target_file") server(s))"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to create ~/.opencode.json"
            return 1
        fi
        return 0
    fi

    if ! jq empty "$target_file" 2>/dev/null; then
        local corrupt_bak="${target_file}.corrupt-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$corrupt_bak"
        log "⚠ ~/.opencode.json was corrupt — saved to $(basename "$corrupt_bak"), recreating"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcp: (.mcp // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
        else
            rm -f "$temp_file"
            return 1
        fi
        return 0
    fi

    local backup_file=""
    if [ "$backup_before_merge" = "true" ]; then
        backup_file="${target_file}.backup-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$backup_file"
        log "✓ Created backup: $(basename "$backup_file")"
        ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true
    fi

    local temp_file
    temp_file=$(mktemp)
    jq -s '
      .[0] as $existing |
      .[1].mcp as $nix |
      $existing | .mcp = (($existing.mcp // {}) + ($nix // {}))
    ' "$target_file" "$nix_file" > "$temp_file" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        log "✓ MCP servers merged into ~/.opencode.json ($(jq '.mcp | length' "$target_file") total)"
    else
        rm -f "$temp_file"
        echo "⚠ Failed to merge MCP servers into ~/.opencode.json — keeping original"
        if [ -n "$backup_file" ] && [ -f "$backup_file" ]; then
            cp "$backup_file" "$target_file"
            echo "✓ Restored from backup"
        fi
        return 1
    fi
}

merge_codex_mcp() {
    local target_file="$1"
    local nix_file="/etc/codex/nix-mcp-servers.toml"

    [ -f "$nix_file" ] || return 0

    if ! command -v python3 &>/dev/null; then
        echo "⚠ python3 not available — skipping Codex MCP merge"
        return 1
    fi

    if ! python3 -c "import tomllib; tomllib.load(open('$nix_file','rb'))" 2>/dev/null; then
        echo "⚠ nix-mcp-servers.toml (Codex) is invalid TOML — skipping MCP merge"
        return 1
    fi

    local backup_before_merge
    backup_before_merge=$(python3 -c "
import tomllib, sys
with open('$nix_file', 'rb') as f:
    d = tomllib.load(f)
print('true' if d.get('backupBeforeMerge', True) else 'false')
" 2>/dev/null || echo "true")

    mkdir -p "$(dirname "$target_file")"

    if [ -f "$target_file" ] && ! python3 -c "import tomllib; tomllib.load(open('$target_file','rb'))" 2>/dev/null; then
        local corrupt_bak="${target_file}.corrupt-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$corrupt_bak"
        log "⚠ ~/.codex/config.toml was corrupt — saved to $(basename "$corrupt_bak"), recreating"
        rm -f "$target_file"
    fi

    local backup_file=""
    if [ -f "$target_file" ] && [ "$backup_before_merge" = "true" ]; then
        backup_file="${target_file}.backup-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$backup_file"
        log "✓ Created backup: $(basename "$backup_file")"
        ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true
    fi

    local temp_file
    temp_file=$(mktemp --suffix=.toml)

    python3 - "$nix_file" "$target_file" "$temp_file" 2>/dev/null << 'PYEOF'
import sys, tomllib, os

nix_path, target_path, temp_path = sys.argv[1], sys.argv[2], sys.argv[3]

def toml_val(v):
    if isinstance(v, str):   return f'"{v}"'
    if isinstance(v, bool):  return 'true' if v else 'false'
    if isinstance(v, int):   return str(v)
    if isinstance(v, float): return repr(v)
    if isinstance(v, list):  return '[' + ', '.join(toml_val(x) for x in v) + ']'
    raise ValueError(f"unsupported type: {type(v)}")

def write_toml(data, out):
    # Scalars first (skip internal keys and tables)
    skip = {'mcp_servers', 'backupBeforeMerge'}
    for k, v in data.items():
        if k not in skip and not isinstance(v, dict):
            out.write(f'{k} = {toml_val(v)}\n')
    # mcp_servers section
    if 'mcp_servers' in data:
        for srv, sdata in data['mcp_servers'].items():
            out.write(f'\n[mcp_servers.{srv}]\n')
            for sk, sv in sdata.items():
                if not isinstance(sv, dict):
                    out.write(f'{sk} = {toml_val(sv)}\n')
            for sk, sv in sdata.items():
                if isinstance(sv, dict):
                    out.write(f'\n[mcp_servers.{srv}.{sk}]\n')
                    for ek, ev in sv.items():
                        out.write(f'{ek} = {toml_val(ev)}\n')
    # Other tables
    for k, v in data.items():
        if k not in skip and isinstance(v, dict):
            out.write(f'\n[{k}]\n')
            for sk, sv in v.items():
                if not isinstance(sv, dict):
                    out.write(f'{sk} = {toml_val(sv)}\n')

with open(nix_path, 'rb') as f:
    nix = tomllib.load(f)

try:
    with open(target_path, 'rb') as f:
        existing = tomllib.load(f)
except FileNotFoundError:
    existing = {}

merged = dict(existing)
merged['mcp_servers'] = {**existing.get('mcp_servers', {}), **nix.get('mcp_servers', {})}

with open(temp_path, 'w') as f:
    write_toml(merged, f)

print(f"merged {len(merged.get('mcp_servers', {}))} server(s)", file=sys.stderr)
PYEOF

    if [ $? -eq 0 ] && [ -s "$temp_file" ] && python3 -c "import tomllib; tomllib.load(open('$temp_file','rb'))" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        log "✓ MCP servers merged into ~/.codex/config.toml"
    else
        rm -f "$temp_file"
        echo "⚠ Failed to merge MCP servers into ~/.codex/config.toml — keeping original"
        if [ -n "$backup_file" ] && [ -f "$backup_file" ]; then
            cp "$backup_file" "$target_file"
            echo "✓ Restored from backup"
        fi
        return 1
    fi
}

# ── Merge nix MCP servers into user config files (additive, nix wins on conflict) ──
merge_claude_mcp "$HOME/.claude.json"
[ -f "$HOME/.claude.json" ] && chown "$HOST_USER" "$HOME/.claude.json"

merge_opencode_providers "$HOME/opencode.json"
[ -f "$HOME/opencode.json" ] && chown "$HOST_USER" "$HOME/opencode.json"

merge_opencode_mcp "$HOME/.opencode.json"
[ -f "$HOME/.opencode.json" ] && chown "$HOST_USER" "$HOME/.opencode.json"

merge_codex_mcp "$HOME/.codex/config.toml"
[ -d "$HOME/.codex" ] && chown -R "$HOST_USER" "$HOME/.codex"

# ── GUI Setup (optional) ──────────────────────────────────────────────────────
if [ "$DEVCELL_GUI_ENABLED" = "true" ]; then
    DISPLAY_NUM=99
    RESOLUTION=1920x1080x24

    mkdir -p /tmp/.X11-unix
    chmod 1777 /tmp/.X11-unix

    log "Starting Xvfb on display :${DISPLAY_NUM}..."
    gosu "$USER" Xvfb :${DISPLAY_NUM} -screen 0 ${RESOLUTION} 2>/dev/null &
    sleep 1

    export DISPLAY=:${DISPLAY_NUM}

    if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
        gosu "$USER" feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
    else
        gosu "$USER" xsetroot -solid '#1e1e2e' 2>/dev/null || true
    fi

    FLUXBOX_RC=/tmp/fluxbox-init
    cp "$DEVCELL_HOME/.fluxbox/init" "$FLUXBOX_RC"
    chmod u+w "$FLUXBOX_RC"
    WORKSPACE_NAME="${APP_NAME:-cell}"
    if grep -q "session.screen0.workspaceNames" "$FLUXBOX_RC"; then
        sed -i "s/^session.screen0.workspaceNames:.*/session.screen0.workspaceNames: ${WORKSPACE_NAME}/" "$FLUXBOX_RC"
    else
        echo "session.screen0.workspaceNames: ${WORKSPACE_NAME}" >> "$FLUXBOX_RC"
    fi
    log "Starting fluxbox (workspace: ${WORKSPACE_NAME})..."
    gosu "$USER" fluxbox -rc "$FLUXBOX_RC" &>/dev/null &
    sleep 1

    if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
        gosu "$USER" feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
    fi

    log "Starting x11vnc on port 5900..."
    gosu "$USER" x11vnc -display :${DISPLAY_NUM} -forever -shared -passwd vnc -rfbport 5900 \
        -desktop "${APP_NAME:-cell}" -pointer_mode 2 -repeat &>/dev/null &

    log "VNC server ready - connect to localhost:${EXT_VNC_PORT:-5900}"
    log "DISPLAY=:${DISPLAY_NUM}"
fi

export CHROMIUM_PROFILE_PATH="${HOME}/.chrome-${APP_NAME:-cell}"
export PLAYWRIGHT_MCP_USER_DATA_DIR="${HOME}/.playwright-${APP_NAME:-cell}"

# Ensure ASDF_DATA_DIR and shims are correct for exec'd processes (e.g. claude)
# that don't source shell rc files and would otherwise inherit the container ENV
# which still points at the ephemeral /opt/asdf.
export ASDF_DATA_DIR="${HOME}/.asdf"
export PATH="${HOME}/.asdf/shims:${PATH}"

exec gosu "$USER" "$@"
