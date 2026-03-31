#!/bin/bash
# 21-secrets.sh — write op-resolved secrets to tmpfs for MCP tools
# Sourced by entrypoint.sh if present and executable.
# Secrets are written to tmpfs (/run/secrets/) — RAM-only, never touches disk.
#
# DEVCELL_SECRET_KEYS is a comma-separated list of env var names resolved
# from [op] items by the runner on the host. The entrypoint writes their
# values to /run/secrets/devcell in dotenv format — usable by any MCP tool
# that supports --secrets (Playwright, etc.).

# Skip if /run/secrets is not mounted (e.g. older containers without tmpfs)
if [ ! -d /run/secrets ]; then
    log "Skipping secrets: /run/secrets not mounted"
    return 0
fi

# Skip if no secret keys declared
if [ -z "$DEVCELL_SECRET_KEYS" ]; then
    log "No DEVCELL_SECRET_KEYS set, skipping secrets"
    return 0
fi

# Atomic write: mktemp → write → chmod → mv
TMP=$(mktemp /run/secrets/devcell.XXXXXX)
IFS=',' read -ra _KEYS <<< "$DEVCELL_SECRET_KEYS"
_COUNT=0
for _key in "${_KEYS[@]}"; do
    _val=$(printenv "$_key" 2>/dev/null)
    if [ -n "$_val" ]; then
        echo "$_key=$_val" >> "$TMP"
        _COUNT=$((_COUNT + 1))
    fi
done
chmod 600 "$TMP"
mv "$TMP" /run/secrets/devcell
chown "$HOST_USER" /run/secrets/devcell
log "Generated /run/secrets/devcell ($_COUNT secrets)"
