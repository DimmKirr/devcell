#!/bin/bash
# 40-postgres.sh — PostgreSQL for CloudQuery asset inventory
# Sourced by entrypoint.sh if present and executable.

NIX_BIN="/opt/devcell/.local/state/nix/profiles/profile/bin"
PGDATA="$HOME/.local/share/postgresql"
PGPORT=5432

# Symlink cloudquery config from nix home (not copy — stays in sync with nix store)
if [ -d "$DEVCELL_HOME/.config/cloudquery" ]; then
    mkdir -p "$HOME/.config"
    ln -sfT "$DEVCELL_HOME/.config/cloudquery" "$HOME/.config/cloudquery"
    chown -h "$HOST_USER" "$HOME/.config/cloudquery"
    log "✓ CloudQuery config linked from nix"
fi

# Handle stale PID from previous container (bind mount persists across restarts)
if [ -f "$PGDATA/postmaster.pid" ]; then
    if ! gosu "$HOST_USER" "$NIX_BIN/pg_isready" -p "$PGPORT" -h /tmp -q 2>/dev/null; then
        log "Removing stale PostgreSQL PID file"
        rm -f "$PGDATA/postmaster.pid"
    fi
fi

# Initialize data directory on first run
if [ ! -f "$PGDATA/PG_VERSION" ]; then
    log "Initializing PostgreSQL data directory"
    mkdir -p "$PGDATA"
    chown "$HOST_USER" "$PGDATA"
    gosu "$HOST_USER" "$NIX_BIN/initdb" -D "$PGDATA" --auth=trust --no-locale -E UTF8
fi

chown -R "$HOST_USER" "$PGDATA"

# Start PostgreSQL as session user (TCP on localhost:5432 + Unix socket in /tmp)
# pg_ctl prints "waiting for server to start..." to stdout — suppress unless debug mode.
if [ "${DEVCELL_DEBUG:-false}" = "true" ]; then
    gosu "$HOST_USER" "$NIX_BIN/pg_ctl" -D "$PGDATA" -l "$PGDATA/postgresql.log" \
        -o "-p $PGPORT -k /tmp" start
else
    gosu "$HOST_USER" "$NIX_BIN/pg_ctl" -D "$PGDATA" -l "$PGDATA/postgresql.log" \
        -o "-p $PGPORT -k /tmp" start >/dev/null 2>&1
fi

# Readiness gate — block until accepting connections (up to 15s)
for _ in $(seq 1 30); do
    gosu "$HOST_USER" "$NIX_BIN/pg_isready" -p "$PGPORT" -h /tmp -q && break
    sleep 0.5
done

# Create cloudquery role and database if missing
if ! gosu "$HOST_USER" "$NIX_BIN/psql" -p "$PGPORT" -h /tmp -d postgres -tAc \
    "SELECT 1 FROM pg_roles WHERE rolname='cloudquery'" | grep -q 1; then
    gosu "$HOST_USER" "$NIX_BIN/psql" -p "$PGPORT" -h /tmp -d postgres -c \
        "CREATE ROLE cloudquery WITH LOGIN PASSWORD 'cloudquery';"
fi

if ! gosu "$HOST_USER" "$NIX_BIN/psql" -p "$PGPORT" -h /tmp -d postgres -tAc \
    "SELECT 1 FROM pg_database WHERE datname='cloudquery'" | grep -q 1; then
    gosu "$HOST_USER" "$NIX_BIN/createdb" -p "$PGPORT" -h /tmp -O cloudquery cloudquery
fi

log "PostgreSQL ready on port $PGPORT"
