#!/bin/bash
# 30-codex.sh — Codex CLI MCP server merge logic
# Sourced by entrypoint.sh; has access to $HOME, $HOST_USER, log()

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

merge_codex_mcp "$HOME/.codex/config.toml"
[ -d "$HOME/.codex" ] && chown -R "$HOST_USER" "$HOME/.codex"
