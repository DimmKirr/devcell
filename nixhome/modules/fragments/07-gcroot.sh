#!/bin/bash
# 07-gcroot.sh — stamp GC roots at container start (CELL-332).
#
# The thin build stamps GC roots during image creation, but containers
# that reuse an existing image (shared config → shared image tag) skip
# the build path entirely. This fragment ensures every running container
# has its profile pinned as a GC root on the shared nix volume.
#
# Runs as root (before gosu drop). Requires /nix to be mounted.

DEVCELL_HOME="/opt/devcell"
HM_PROFILE=$(readlink -f "$DEVCELL_HOME/.local/state/nix/profiles/profile" 2>/dev/null)

if [ -z "$HM_PROFILE" ] || [ ! -d "$HM_PROFILE" ]; then
    log "gcroot: no resolved profile — skipping GC root stamp"
    return 0
fi

HM_PROFILE_HASH=$(basename "$HM_PROFILE" | cut -d- -f1)
HM_GENERATION=$(readlink -f "$DEVCELL_HOME/.local/state/nix/profiles/home-manager" 2>/dev/null)

mkdir -p /nix/var/nix/gcroots/devcell

ln -sfT "$HM_PROFILE" "/nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-profile"
log "gcroot: stamped ${HM_PROFILE_HASH}-profile → $HM_PROFILE"

if [ -n "$HM_GENERATION" ] && [ -d "$HM_GENERATION" ]; then
    ln -sfT "$HM_GENERATION" "/nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-generation"
    log "gcroot: stamped ${HM_PROFILE_HASH}-generation → $HM_GENERATION"
fi

# Stamp metadata for reaper and drift detection (CELL-334).
_project_name="${DEVCELL_PROJECT_NAME:-unknown}"
cat > "/nix/var/nix/gcroots/devcell/${HM_PROFILE_HASH}-meta" <<METAEOF
project=${_project_name}
stack=${DEVCELL_STACK:-unknown}
modules=${DEVCELL_MODULES:-}
profile=${HM_PROFILE}
stamped=$(date -u +%Y-%m-%dT%H:%M:%SZ)
METAEOF
log "gcroot: wrote ${HM_PROFILE_HASH}-meta (project=${_project_name})"

# ── Drift detection (CELL-334) ──────────────────────────────────────────
# Compare this container's store hash against other active roots.
# Different hashes with the same stack indicate lock drift — different
# nixpkgs revisions producing duplicate base packages on disk.
_root_count=0
_hash_list=""
for _root in /nix/var/nix/gcroots/devcell/*-profile; do
    [ -L "$_root" ] || continue
    _h=$(basename "$_root" | sed 's/-profile$//')
    _root_count=$((_root_count + 1))
    case " $_hash_list " in
        *" $_h "*) ;;
        *) _hash_list="$_hash_list $_h" ;;
    esac
done
_unique_hashes=$(echo "$_hash_list" | wc -w)

if [ "$_unique_hashes" -gt 1 ]; then
    log "gcroot: drift detected — $_unique_hashes unique profile hashes across $_root_count roots"
    if [ "${DEVCELL_DEBUG:-false}" = "true" ]; then
        for _meta in /nix/var/nix/gcroots/devcell/*-meta; do
            [ -f "$_meta" ] || continue
            _h=$(basename "$_meta" | sed 's/-meta$//')
            _proj=$(grep '^project=' "$_meta" 2>/dev/null | cut -d= -f2)
            _stack=$(grep '^stack=' "$_meta" 2>/dev/null | cut -d= -f2)
            log "  root $_h: project=$_proj stack=$_stack"
        done
        log "gcroot: rebuild all cells with the same flake.lock to converge and reclaim disk"
    else
        echo "warning: nix config drift detected ($_unique_hashes variants) — run with --debug for details, rebuild to converge"
    fi
fi
