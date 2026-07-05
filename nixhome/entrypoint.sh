#!/bin/bash
# Entrypoint script — runs as root, drops to HOST_USER via gosu at the end.
#
# MINIMAL BOOTSTRAP ONLY. All tool-specific logic lives in nix-generated
# fragments sourced from /etc/devcell/entrypoint.d/ — see nixhome/modules/.
#
# /opt/devcell  — nix environment home (owned by devcell, read-only for session user)
# /home/$HOST_USER — session user's personal home (writable)

DEVCELL_HOME="/opt/devcell"
REPO_HOMEDIR="${WORKSPACE}/homedir"
HOST_USER="${HOST_USER:-devcell}"
export USER="$HOST_USER"
export HOME="/home/$HOST_USER"

# ── Verbose logging — only active when DEVCELL_DEBUG=true ─────────────────────
_ENTRYPOINT_T0=$(($(date +%s%N) / 1000000))
if [ "${DEVCELL_DEBUG:-false}" = "true" ]; then
    log() {
        local _ms=$(( $(date +%s%N) / 1000000 - _ENTRYPOINT_T0 ))
        printf '[%d.%03ds] %s\n' $((_ms/1000)) $((_ms%1000)) "$*"
    }
else
    log() { :; }
fi

log "Entrypoint start (user=$HOST_USER app=${APP_NAME:-})"

# CELL-264: minimal inline notify so we can emit progress BEFORE 00-notify.sh
# is sourced. Same shape as the full helper — just touch a sentinel file in
# the bind-mounted boot dir. The full notify() function (with sanity checks)
# is defined when 00-notify.sh is sourced below, but for the bootstrap rows
# we need it available immediately. Idempotent — same definition is OK.
notify() {
    [ -n "$DEVCELL_BOOT_DIR" ] && [ -d "$DEVCELL_BOOT_DIR" ] || return 0
    touch "$DEVCELL_BOOT_DIR/$1" 2>/dev/null || true
}

# First visible signal: container is running entrypoint. Fills the gap between
# the host's "Cell ready" and the first fragment-emitted row — accounts for
# Docker's container creation (~200-1000ms on Docker Desktop) + entrypoint
# bootstrap (user creation, dotfile copy, etc.).
notify container.ready

# Read build metadata. /etc/devcell/metadata.json holds STATIC info (stack,
# package count) that's stable across rebuilds. Per-build provenance (date,
# git rev) comes from the runner-injected DEVCELL_BUILD_DATE / DEVCELL_BUILD_REV
# env vars — sourced from the OCI manifest's labels at `cell ...` launch time.
# Pre-2026-05-16 images don't set those env vars; we fall back to the file
# values so older containers don't lose info.
if [ -f /etc/devcell/metadata.json ] && command -v jq &>/dev/null; then
    _meta_base=$(jq -r '.base_image // "unknown"' /etc/devcell/metadata.json 2>/dev/null)
    _meta_stack=$(jq -r '.stack // ""' /etc/devcell/metadata.json 2>/dev/null)
    _meta_modules=$(jq -r '.modules // [] | join(",")' /etc/devcell/metadata.json 2>/dev/null)
    _meta_pkgs=$(jq -r '.packages // 0' /etc/devcell/metadata.json 2>/dev/null)
    # Per-build provenance: prefer runner-injected env (real values from
    # OCI labels at launch time), fall back to placeholder JSON fields.
    _meta_commit="${DEVCELL_BUILD_REV:-$(jq -r '.git_commit // "unknown"' /etc/devcell/metadata.json 2>/dev/null)}"
    _meta_date="${DEVCELL_BUILD_DATE:-$(jq -r '.build_date // ""' /etc/devcell/metadata.json 2>/dev/null)}"
    log "Base image: $_meta_base"
    log "User image: $_meta_commit${_meta_date:+ built $_meta_date}${DEVCELL_IMAGE:+ (tag: $DEVCELL_IMAGE)}"
    log "Stack: $_meta_stack | Modules: ${_meta_modules:-none} | Nix packages: $_meta_pkgs"
else
    log "Base image: $(cat /etc/devcell/base-image-version 2>/dev/null || echo 'unknown')"
    log "User image: $(cat /etc/devcell/user-image-version 2>/dev/null || echo 'unknown')"
fi

# ── Create session user if needed ─────────────────────────────────────────────
if ! id "$HOST_USER" &>/dev/null; then
    useradd -m -s /bin/zsh "$HOST_USER" 2>/dev/null
    echo "$HOST_USER ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
fi

# ── Grant docker socket access to session user ────────────────────────────────
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
# Symlink cell binary so it's on the session user's PATH
# (shell rc rewrites /opt/devcell → $HOME, so /opt/devcell/.local/bin is not in PATH)
ln -sf /opt/devcell/.local/bin/cell "$HOME/.local/bin/cell" 2>/dev/null || true
chown -h "$HOST_USER" "$HOME/.local" "$HOME/.local/bin" "$HOME/tmp"

# ── Isolate GPG per container ────────────────────────────────────────────────
# Persistent $HOME is shared across containers. GnuPG 2.4+ uses keyboxd with
# SQLite which breaks under concurrent access from different PID namespaces.
# Redirect GNUPGHOME to container-local storage to avoid lock contention.
export GNUPGHOME="$HOME/tmp/.gnupg"
mkdir -p "$GNUPGHOME"
chmod 700 "$GNUPGHOME"
chown "$HOST_USER" "$GNUPGHOME"

# ── Clean up stale nix-store symlinks from persistent $HOME ──────────────────
# $HOME is a persistent bind mount. Home-manager symlinks from previous image
# builds dangle after nix GC removes old store paths. Remove them so fragments
# can write fresh configs.
if [ -d "$HOME" ]; then
    find "$HOME" -maxdepth 4 -type l -not -path "*/tmp/*" 2>/dev/null | while IFS= read -r _link; do
        _target=$(readlink "$_link" 2>/dev/null)
        case "$_target" in /nix/store/*)
            if [ ! -e "$_link" ]; then
                log "Removing stale symlink: $_link -> $_target"
                rm -f "$_link"
            fi
        ;; esac
    done
fi

# Second visible signal: pre-fragment bootstrap done (user/dotfiles/GPG/etc.
# are wired up). The fragment loop is about to start running real work.
notify entrypoint.ready

# ── Source entrypoint fragments (nix-generated) ──────────────────────────────
# Modules drop shell scripts into /etc/devcell/entrypoint.d/ via home-manager.
# Each fragment guards its own preconditions (e.g. DEVCELL_GUI_ENABLED).
#
# Fragment numbering convention:
#   05-* — shell rc + nix profile setup
#   10-* — runtime setup (mise)
#   20-* — home directory setup (homedir, browser env)
#   30-* — tool config merges (claude, opencode, codex)
#   50-* — services (GUI, xrdp)
if [ -d /etc/devcell/entrypoint.d ]; then
    for f in /etc/devcell/entrypoint.d/*.sh; do
        [ -x "$f" ] && { . "$f" || log "⚠ Fragment failed: $f (exit $?)"; }
    done
fi

# ── Fix ownership for files created by fragments ──────────────────────────────
# Fragments run as root and may create files in $HOME without chown.
# Catch any missed files (max depth 2 to avoid expensive deep traversal).
find "$HOME" -maxdepth 2 -user root -not -path "*/tmp/*" -exec chown "$HOST_USER" {} + 2>/dev/null || true

log "Entrypoint ready — exec $*"

# CELL-263: signal host that boot is complete — sealing handoff. notify is
# defined by /etc/devcell/entrypoint.d/00-notify.sh which is the first
# fragment sourced above; harmless no-op when NOTIFY_SOCKET is unset.
command -v notify >/dev/null 2>&1 && notify boot.ready

exec gosu "$USER" "$@"
