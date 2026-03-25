#!/bin/bash
# 30-opencode.sh — merge nix-declared OpenCode providers + MCP servers
# Sourced by entrypoint.sh; has access to $HOME, $HOST_USER, log()

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

# ── Run merges ──
merge_opencode_providers "$HOME/opencode.json"
[ -f "$HOME/opencode.json" ] && chown $HOST_USER "$HOME/opencode.json"

merge_opencode_mcp "$HOME/.opencode.json"
[ -f "$HOME/.opencode.json" ] && chown $HOST_USER "$HOME/.opencode.json"
