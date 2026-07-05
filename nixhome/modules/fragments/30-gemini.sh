#!/bin/bash
# 30-gemini.sh — Gemini CLI MCP server merge logic
# Sourced by entrypoint.sh; has access to $HOME, $HOST_USER, log()
#
# Gemini's ~/.gemini/settings.json holds `mcpServers` at top level — same
# shape as Claude's ~/.claude.json — so the merge mirrors 30-claude.sh,
# differing only in target paths.

notify gemini.starting

merge_gemini_mcp() {
    local target_file="$1"
    local nix_file="/etc/gemini/nix-mcp-servers.json"

    [ -f "$nix_file" ] || return 0

    if ! jq empty "$nix_file" 2>/dev/null; then
        echo "⚠ nix-mcp-servers.json (Gemini) is invalid JSON — skipping MCP merge"
        return 1
    fi

    local backup_before_merge
    backup_before_merge=$(jq -r '.backupBeforeMerge // true' "$nix_file")

    mkdir -p "$(dirname "$target_file")"

    # Fresh start — no existing settings.json.
    if [ ! -f "$target_file" ]; then
        log "Creating ~/.gemini/settings.json with nix MCP servers"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcpServers: (.mcpServers // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/.gemini/settings.json created ($(jq '.mcpServers | length' "$target_file") server(s))"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to create ~/.gemini/settings.json from nix MCP servers"
            return 1
        fi
        return 0
    fi

    # Existing file is corrupt — back it up and recreate.
    if ! jq empty "$target_file" 2>/dev/null; then
        local corrupt_bak="${target_file}.corrupt-$(date +%Y%m%d-%H%M%S)"
        cp "$target_file" "$corrupt_bak"
        log "⚠ ~/.gemini/settings.json was corrupt — saved to $(basename "$corrupt_bak"), recreating"
        local temp_file
        temp_file=$(mktemp)
        jq '{mcpServers: (.mcpServers // {})}' "$nix_file" > "$temp_file"
        if [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            log "✓ ~/.gemini/settings.json recreated"
        else
            rm -f "$temp_file"
            echo "⚠ Failed to recreate ~/.gemini/settings.json"
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
        ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true
    fi

    # Merge: remove stale nix-managed servers (command starts with /opt/devcell/),
    # then add current stack's servers. User-defined servers preserved.
    local temp_file
    temp_file=$(mktemp)
    jq -s '
      .[0] as $existing |
      .[1].mcpServers as $nix |
      (($existing.mcpServers // {}) | to_entries |
        map(select(.value.command == null or (.value.command | startswith("/opt/devcell/") | not))) |
        from_entries) as $cleaned |
      $existing | .mcpServers = ($cleaned + ($nix // {}))
    ' "$target_file" "$nix_file" > "$temp_file" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "$temp_file" ] && jq empty "$temp_file" 2>/dev/null; then
        mv "$temp_file" "$target_file"
        log "✓ MCP servers merged into ~/.gemini/settings.json ($(jq '.mcpServers | length' "$target_file") total)"
    else
        rm -f "$temp_file"
        echo "⚠ Failed to merge MCP servers into ~/.gemini/settings.json — keeping original"
        if [ -n "$backup_file" ] && [ -f "$backup_file" ]; then
            cp "$backup_file" "$target_file"
            echo "✓ Restored from backup"
        fi
        return 1
    fi
}

merge_gemini_mcp "$HOME/.gemini/settings.json"
[ -d "$HOME/.gemini" ] && chown -R "$HOST_USER" "$HOME/.gemini"

notify gemini.ready
