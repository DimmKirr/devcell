# CELL-264: in-container boot progress helper.
#
# Sourced first by the entrypoint so all later fragments can call notify()
# to report what they're doing. The host (cell CLI) watches $DEVCELL_BOOT_DIR
# via fsnotify and renders each sentinel file CREATE as a ✓ row on the user's
# terminal between the host-side checklist and the final TTY handoff.
#
# Naming convention for sentinels:
#
#   <component>.starting   — fragment is beginning meaningful work
#   <component>.ready      — fragment finished cleanly
#   boot.ready             — entrypoint.sh just before exec into the agent
#                            binary (seal event; host stops rendering)
#
# Usage:
#   notify mise.starting
#   ...do work...
#   notify mise.ready
#
# Transport: a directory bind-mounted from the host. Universal Docker
# compatibility — works on Linux native, macOS Docker Desktop, Windows
# Docker Desktop, Lima, OrbStack. Replaces CELL-263 sd_notify socket
# bind-mounts which were unreliable through Docker Desktop's virtiofs.
#
# Failure modes — all silent, fragment boot must never abort because of
# missing progress reporting:
#   - $DEVCELL_BOOT_DIR unset → host not listening (older cell, manual exec) — no-op
#   - dir missing → host listener crashed mid-boot — no-op
#   - touch fails (no space / EROFS) → no-op, container keeps booting

notify() {
    [ -n "$DEVCELL_BOOT_DIR" ] && [ -d "$DEVCELL_BOOT_DIR" ] || return 0
    touch "$DEVCELL_BOOT_DIR/$1" 2>/dev/null || true
}
