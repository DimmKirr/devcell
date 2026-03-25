#!/bin/bash
# 30-claude.sh — Claude Code merge logic (nix-generated entrypoint fragment)
# Sourced by entrypoint.sh; has access to: $HOME, $HOST_USER, $USER, $DEVCELL_HOME, log()

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
        rsync -a --chmod=+x --chown="$HOST_USER" --delete \
            "$nix_hooks_dir/" "$HOME/.claude/hooks/"
        log "✓ Claude hooks synced from nix"
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

# Run nix hooks/settings merge
merge_claude_nix

# Merge nix MCP servers into user config
merge_claude_mcp "$HOME/.claude.json"
[ -f "$HOME/.claude.json" ] && chown "$HOST_USER" "$HOME/.claude.json"
