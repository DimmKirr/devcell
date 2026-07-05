#!/bin/bash
# 50-gui.sh — GUI service startup (Xvfb, fluxbox, x11vnc, xrdp)
# Sourced by entrypoint.sh if present and executable.

[ "$DEVCELL_GUI_ENABLED" = "true" ] || return 0

notify gui.starting

# Ensure DBUS machine-id exists (Kitty/GTK apps need it)
[ -f /etc/machine-id ] || dbus-uuidgen > /etc/machine-id 2>/dev/null || true

DISPLAY_NUM=99
RESOLUTION=1920x1080x24

mkdir -p /tmp/.X11-unix
chmod 1777 /tmp/.X11-unix

# Mesa llvmpipe software rendering — enables GLX for GPU terminals (Kitty etc.)
export LIBGL_ALWAYS_SOFTWARE=1
export GALLIUM_DRIVER=llvmpipe
export LIBGL_DRIVERS_PATH="/opt/devcell/.mesa-dri"

# Root-cause fix moved to 06-nix-ldpath.sh + 05-shell-rc.sh: on pure images
# the closure-based LD_LIBRARY_PATH is no longer exported at all (it was a
# Debian-base bootstrap that breaks versioned symbols on nix-built binaries
# like uv's `_rjem_malloc` and x11vnc's libgpg-error). Kept as a no-op
# placeholder so we don't have to rewrite every gosu invocation below — in
# pure mode LD_LIBRARY_PATH is unset, so `env -u` is a harmless idempotent
# strip; in legacy Debian mode it preserves the existing LD bootstrap for
# debian-style GUI services.
_NIX_ENV="env -u LD_LIBRARY_PATH -u _DEVCELL_LD_SET"

# ── Restart-resilient service startup ─────────────────────────────────────
# Each service: kill any stale instance, clean its lockfile/socket, then
# spawn fresh with setsid + logfile. Idempotent — sourcing this fragment
# twice (e.g. after container restart, manual reload) won't leave duplicate
# processes or fail with "address already in use" / "Server already active".

log "Starting Xvfb on display :${DISPLAY_NUM} (+GLX, Mesa llvmpipe)..."
# Kill any stale Xvfb owned by the session user, remove its lockfile.
# Xvfb refuses to start if /tmp/.X<N>-lock exists ("Server is already
# active for display N") — happens on container restart with /tmp tmpfs.
pkill -u "$USER" -x Xvfb 2>/dev/null
sleep 0.2
rm -f /tmp/.X${DISPLAY_NUM}-lock /tmp/.X11-unix/X${DISPLAY_NUM}
# setsid → Xvfb survives entrypoint's `exec gosu <cmd>`. Without it, the
# inherited controlling TTY makes Xvfb killable by signals the foreground
# `cmd` (e.g. claude TUI) sends to its process group.
# Output → /tmp/Xvfb.log so a crash isn't silent.
setsid gosu "$USER" $_NIX_ENV Xvfb :${DISPLAY_NUM} -screen 0 ${RESOLUTION} -dpi 96 +extension GLX +render +iglx \
    < /dev/null > /tmp/Xvfb.log 2>&1 &
export DISPLAY=:${DISPLAY_NUM}
# Wait for X server to accept connections (socket file appears before server is ready)
for i in $(seq 1 40); do
    xset -display :${DISPLAY_NUM} q >/dev/null 2>&1 && break
    sleep 0.05
done

# Load X resources (xterm dark theme, cursor color, fonts)
# Deferred via background process: xrdb ChangeProperty requests sent from
# the entrypoint's PID 1 context are silently dropped by Xvfb. Running
# xrdb from a detached process after exec gosu replaces PID 1 works.
# PulseAudio null sink — provides audio backend for Chromium AudioContext.
# Without this, AudioContext produces silent (all-zero) frequency data,
# which is a bot detection signal (CreepJS).
# Uses -n (no default config) to avoid dbus dependency; explicitly loads
# native-protocol-unix (socket) + null-sink (virtual audio output).
PULSE_DIR="/tmp/pulse-runtime"
mkdir -p "$PULSE_DIR"
chown "$USER:$(id -gn "$USER")" "$PULSE_DIR"
log "Starting PulseAudio (null sink)..."
XRDP_PULSE_MOD=$(find "$(dirname "$(command -v pulseaudio)")/../lib" -name 'module-xrdp-sink.so' 2>/dev/null | head -1)
_PA_DL_SEARCH=()
if [ -n "$XRDP_PULSE_MOD" ]; then
    XRDP_PULSE_DIR=$(dirname "$XRDP_PULSE_MOD")
    _PA_DL_SEARCH=(--dl-search-path="$XRDP_PULSE_DIR")
    log "  xrdp audio sink found: $XRDP_PULSE_MOD (deferred until chansrv ready)"
fi
PULSE_LOG="/var/log/pulseaudio.log"
touch "$PULSE_LOG" && chown "$USER" "$PULSE_LOG"
gosu "$USER" env -u LD_LIBRARY_PATH -u _DEVCELL_LD_SET XDG_RUNTIME_DIR="$PULSE_DIR" \
    pulseaudio --daemonize=yes --exit-idle-time=-1 --disable-shm=true -n \
    --log-target=file:"$PULSE_LOG" --log-level=info \
    "${_PA_DL_SEARCH[@]}" \
    --load="module-null-sink sink_name=NullSink" \
    --load="module-native-protocol-unix" 2>/dev/null
PA_RC=$?
export PULSE_SERVER="unix:$PULSE_DIR/pulse/native"
export XDG_RUNTIME_DIR="$PULSE_DIR"
if [ $PA_RC -eq 0 ]; then
    log "  PulseAudio started (pid $(cat "$PULSE_DIR/pulse/pid" 2>/dev/null || echo '?'), log: $PULSE_LOG)"
    gosu "$USER" env PULSE_SERVER="unix:$PULSE_DIR/pulse/native" XDG_RUNTIME_DIR="$PULSE_DIR" \
        pactl list sinks short 2>/dev/null | while read -r _idx _name _mod _fmt _state; do
            log "  Sink #${_idx}: ${_name} [${_state}]"
        done
else
    log "  ERROR: PulseAudio failed to start (exit $PA_RC) — check $PULSE_LOG"
fi

# Persist PulseAudio env vars for shells spawned outside the entrypoint
# (docker exec, IDE attach, RDP terminal sessions). 05-shell-rc.sh writes
# the base rc files with > before we run, so >> here is safe and idempotent
# across container restarts (05-shell-rc.sh overwrites first, we append).
if [ $PA_RC -eq 0 ]; then
    _PA_RC_BLOCK="
# -- PulseAudio (appended by 50-gui.sh) --
export PULSE_SERVER=\"unix:$PULSE_DIR/pulse/native\"
export XDG_RUNTIME_DIR=\"$PULSE_DIR\""
    for _rcfile in .zshrc .zshenv .profile .bashrc; do
        [ -f "$HOME/$_rcfile" ] && echo "$_PA_RC_BLOCK" >> "$HOME/$_rcfile"
    done
fi

if [ -f "$DEVCELL_HOME/.Xresources" ]; then
    (sleep 1; xrdb -display :${DISPLAY_NUM} -merge "$DEVCELL_HOME/.Xresources" 2>/dev/null) &
    disown
fi

if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
    gosu "$USER" $_NIX_ENV feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
else
    gosu "$USER" $_NIX_ENV xsetroot -solid '#1e1e2e' 2>/dev/null || true
fi

FLUXBOX_RC=/tmp/fluxbox-init
cp "$DEVCELL_HOME/.fluxbox/init" "$FLUXBOX_RC"
chmod u+w "$FLUXBOX_RC"
WORKSPACE_NAME=" ${APP_NAME:-cell} "
if grep -q "session.screen0.workspaceNames" "$FLUXBOX_RC"; then
    sed -i "s/^session.screen0.workspaceNames:.*/session.screen0.workspaceNames: ${WORKSPACE_NAME}/" "$FLUXBOX_RC"
else
    echo "session.screen0.workspaceNames: ${WORKSPACE_NAME}" >> "$FLUXBOX_RC"
fi
log "Starting fluxbox (workspace: ${WORKSPACE_NAME})..."
# Kill any stale fluxbox before starting fresh.
pkill -u "$USER" -x fluxbox 2>/dev/null
sleep 0.2
# setsid + log file: same reason as Xvfb above — survive entrypoint exec
# and surface crashes via /tmp/fluxbox.log.
setsid gosu "$USER" $_NIX_ENV fluxbox -rc "$FLUXBOX_RC" \
    < /dev/null > /tmp/fluxbox.log 2>&1 &
# Poll for fluxbox process instead of fixed sleep 1
for i in $(seq 1 20); do
    pgrep -u "$USER" fluxbox >/dev/null 2>&1 && break
    sleep 0.05
done

if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
    gosu "$USER" $_NIX_ENV feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
fi

log "Starting x11vnc on port 5900..."
# Kill any stale x11vnc so we don't fight for port 5900. Pause briefly so
# the kernel releases the bind before our new instance attempts it.
pkill -u "$USER" -x x11vnc 2>/dev/null
sleep 0.2
# WHY setsid: without it, x11vnc inherits the entrypoint's controlling TTY.
# When the entrypoint later `exec`s gosu+<cmd> (e.g. claude — a TUI doing
# its own TTY signal handling), x11vnc dies as a side effect → xrdp's
# vnc-any backend can't reach 127.0.0.1:5900 → user sees "operation now
# in progress" / "error connecting to user session" on the RDP client.
# Reproduced 2026-05-15: this fix unblocks RDP-into-cell after the pure flip.
# WHY log file: was &>/dev/null, which made the silent death undebuggable.
setsid gosu "$USER" $_NIX_ENV x11vnc -display :${DISPLAY_NUM} -forever -nevershared -passwd vnc -rfbport 5900 \
    -desktop "${APP_NAME:-cell}" -pointer_mode 2 -repeat -xrandr \
    < /dev/null > /tmp/x11vnc.log 2>&1 &

log "VNC server ready - connect to localhost:${EXT_VNC_PORT:-5900}"
log "DISPLAY=:${DISPLAY_NUM}"

# ── xrdp (RDP gateway to existing VNC session) ────────────────────────
# Set a system password so RDP clients can authenticate via PAM.
# The password is not security-sensitive — the container is already isolated.
# useradd creates accounts with locked passwords (! in shadow) which blocks
# chpasswd via pam_unix. Use usermod -p with a pre-hashed password instead.
usermod -p "$(openssl passwd -6 rdp)" "$HOST_USER" 2>/dev/null || true

XRDP_BIN=$(command -v xrdp 2>/dev/null)
if [ -n "$XRDP_BIN" ]; then
    XRDP_CFG="/tmp/xrdp"
    mkdir -p "$XRDP_CFG"
    XRDP_PREFIX=$(dirname "$(dirname "$(readlink -f "$XRDP_BIN")")")

    # Copy default configs from nix store (read-only) to writable dir
    cp -a "$XRDP_PREFIX/etc/xrdp/"* "$XRDP_CFG/" 2>/dev/null || true
    chmod u+w "$XRDP_CFG/"* 2>/dev/null || true
    # chansrv reads sesman.ini from /etc/xrdp/ (hardcoded path)
    ln -sfT "$XRDP_CFG" /etc/xrdp 2>/dev/null || true

    # Pre-generate RSA keys so xrdp can read them at startup
    # (without this, xrdp fails with "cannot read rsakeys.ini" on first connect)
    if [ "$DEVCELL_DEBUG" = "true" ]; then
        xrdp-keygen xrdp "$XRDP_CFG/rsakeys.ini" || true
    else
        xrdp-keygen xrdp "$XRDP_CFG/rsakeys.ini" >/dev/null 2>&1 || true
    fi

    # Generate self-signed SSL cert in global config dir
    # (survives container restarts via ~/.config/devcell/ bind mount at /etc/devcell/config/)
    XRDP_CERT_DIR="/etc/devcell/config/xrdp"
    mkdir -p "$XRDP_CERT_DIR"
    if [ ! -f "$XRDP_CERT_DIR/key.pem" ]; then
        if [ "$DEVCELL_DEBUG" = "true" ]; then
            openssl req -x509 -newkey rsa:2048 -nodes \
                -keyout "$XRDP_CERT_DIR/key.pem" -out "$XRDP_CERT_DIR/cert.pem" \
                -days 365 -subj "/CN=devcell"
        else
            openssl req -x509 -newkey rsa:2048 -nodes \
                -keyout "$XRDP_CERT_DIR/key.pem" -out "$XRDP_CERT_DIR/cert.pem" \
                -days 365 -subj "/CN=devcell" >/dev/null 2>&1
        fi
    fi

    # Patch xrdp.ini: port, SSL, autorun, logging to file only
    # DEVCELL_DEBUG=true → INFO logs; otherwise WARNING only
    if [ "$DEVCELL_DEBUG" = "true" ]; then
        XRDP_LOG_LEVEL="INFO"
    else
        XRDP_LOG_LEVEL="WARNING"
    fi
    sed -i \
        -e "s|^port=.*|port=3389|" \
        -e "s|^certificate=.*|certificate=$XRDP_CERT_DIR/cert.pem|" \
        -e "s|^key_file=.*|key_file=$XRDP_CERT_DIR/key.pem|" \
        -e "s|^security_layer=.*|security_layer=none|" \
        -e "s|^autorun=.*|autorun=vnc-any|" \
        -e "s|^max_bpp=.*|max_bpp=24|" \
        -e "s|^allow_channels=.*|allow_channels=true|" \
        -e "s|^LogFile=.*|LogFile=/var/log/xrdp.log|" \
        -e "s|^LogLevel=.*|LogLevel=$XRDP_LOG_LEVEL|" \
        -e "s|^#*EnableSyslog=.*|EnableSyslog=false|" \
        -e "s|^#*default_dpi=.*|default_dpi=96|" \
        -e "s|^cliprdr=.*|cliprdr=true|" \
        "$XRDP_CFG/xrdp.ini"

    # Remove stock [Xorg] section (has username=ask which forces login
    # prompt even with autorun). Keep only our [vnc-any] with hardcoded
    # creds so xrdp auto-connects without asking.
    sed -i '/^\[Xorg\]/,/^\[/{ /^\[vnc-any\]/!d; }' "$XRDP_CFG/xrdp.ini"

    # Replace [vnc-any] section — hardcoded creds skip login prompt
    sed -i '/^\[vnc-any\]/,$d' "$XRDP_CFG/xrdp.ini"
    {
        echo '[vnc-any]'
        echo 'name=VNC'
        echo 'lib=libvnc.so'
        echo 'ip=127.0.0.1'
        echo 'port=5900'
        echo "username=${HOST_USER}"
        echo 'password=vnc'
        echo 'xserverbpp=24'
        _CHANSRV_UID=$(id -u "$HOST_USER")
        echo "chansrvport=/var/run/xrdp/${_CHANSRV_UID}/xrdp_chansrv_socket_${DISPLAY_NUM}"
    } >> "$XRDP_CFG/xrdp.ini"

    # xrdp 0.10.x delegates channel handling (rdpsnd, cliprdr) to chansrv.
    # In sesman-managed sessions, sesexec starts chansrv automatically.
    # VNC proxy mode bypasses sesman, so start chansrv manually.
    XRDP_CHANSRV="$XRDP_PREFIX/sbin/xrdp-chansrv"
    if [ -x "$XRDP_CHANSRV" ]; then
        XRDP_RUN_DIR="/var/run/xrdp/${_CHANSRV_UID}"
        mkdir -p "$XRDP_RUN_DIR"
        chown "$HOST_USER:$(id -gn "$HOST_USER")" "$XRDP_RUN_DIR"
        log "Starting xrdp-chansrv for display :${DISPLAY_NUM}..."
        setsid gosu "$HOST_USER" $_NIX_ENV env DISPLAY=:${DISPLAY_NUM} \
            HOME="/home/$HOST_USER" \
            CHANSRV_LOG_PATH="/tmp" \
            PULSE_SERVER="unix:$PULSE_DIR/pulse/native" \
            XDG_RUNTIME_DIR="$PULSE_DIR" \
            "$XRDP_CHANSRV" \
            < /dev/null > /tmp/chansrv.log 2>&1 &
        _CHANSRV_SOCK="$XRDP_RUN_DIR/xrdp_chansrv_socket_${DISPLAY_NUM}"
        for i in $(seq 1 40); do
            [ -S "$_CHANSRV_SOCK" ] && break
            sleep 0.1
        done
        if [ -S "$_CHANSRV_SOCK" ]; then
            log "  chansrv socket ready"
            if [ -n "$XRDP_PULSE_MOD" ]; then
                _PA_RUN="gosu $HOST_USER $_NIX_ENV env PULSE_SERVER=unix:$PULSE_DIR/pulse/native XDG_RUNTIME_DIR=$PULSE_DIR"
                $_PA_RUN pactl load-module module-xrdp-sink xrdp_socket_path=$XRDP_RUN_DIR 2>/dev/null && {
                    $_PA_RUN pactl set-default-sink xrdp-sink 2>/dev/null || true
                    log "  xrdp-sink loaded and set as default"
                } || log "  WARNING: failed to load module-xrdp-sink"
            fi
        else
            log "  WARNING: chansrv socket not found after 4s — check /tmp/chansrv.log"
        fi
    fi

    log "Starting xrdp on port 3389 (RDP → VNC :${DISPLAY_NUM})..."
    # Kill any stale xrdp (defunct or running) before binding 3389.
    pkill -x xrdp 2>/dev/null
    sleep 0.2
    # setsid: same TTY-detachment as Xvfb/x11vnc/fluxbox above.
    # /var/log/xrdp.log: xrdp's traditional log target (read by sysadmins
    # debugging RDP issues). Stays at /var/log because that path is
    # documented in xrdp.ini's LogFile setting.
    setsid $_NIX_ENV xrdp --nodaemon --config "$XRDP_CFG/xrdp.ini" \
        < /dev/null >> /var/log/xrdp.log 2>&1 &

    log "xrdp ready - connect to localhost:${EXT_RDP_PORT:-3389}"
else
    log "xrdp not found — skipping RDP server"
fi

notify gui.ready
