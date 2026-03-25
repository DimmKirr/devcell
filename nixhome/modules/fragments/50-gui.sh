#!/bin/bash
# 50-gui.sh — GUI service startup (Xvfb, fluxbox, x11vnc, xrdp)
# Sourced by entrypoint.sh if present and executable.

[ "$DEVCELL_GUI_ENABLED" = "true" ] || return 0

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
# LD_LIBRARY_PATH inherited from 06-nix-ldpath.sh (full profile closure)

log "Starting Xvfb on display :${DISPLAY_NUM} (+GLX, Mesa llvmpipe)..."
gosu "$USER" Xvfb :${DISPLAY_NUM} -screen 0 ${RESOLUTION} -dpi 96 +extension GLX +render +iglx 2>/dev/null &
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
gosu "$USER" env XDG_RUNTIME_DIR="$PULSE_DIR" \
    pulseaudio --daemonize=yes --exit-idle-time=-1 --disable-shm=true -n \
    --load="module-null-sink sink_name=NullSink" \
    --load="module-native-protocol-unix" 2>/dev/null || true
export PULSE_SERVER="unix:$PULSE_DIR/pulse/native"
export XDG_RUNTIME_DIR="$PULSE_DIR"

if [ -f "$DEVCELL_HOME/.Xresources" ]; then
    (sleep 1; xrdb -display :${DISPLAY_NUM} -merge "$DEVCELL_HOME/.Xresources" 2>/dev/null) &
    disown
fi

if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
    gosu "$USER" feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
else
    gosu "$USER" xsetroot -solid '#1e1e2e' 2>/dev/null || true
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
gosu "$USER" fluxbox -rc "$FLUXBOX_RC" &>/dev/null &
# Poll for fluxbox process instead of fixed sleep 1
for i in $(seq 1 20); do
    pgrep -u "$USER" fluxbox >/dev/null 2>&1 && break
    sleep 0.05
done

if [ -f "$DEVCELL_HOME/.fluxbox/wallpaper.png" ]; then
    gosu "$USER" feh --bg-fill "$DEVCELL_HOME/.fluxbox/wallpaper.png" 2>/dev/null || true
fi

log "Starting x11vnc on port 5900..."
gosu "$USER" x11vnc -display :${DISPLAY_NUM} -forever -nevershared -passwd vnc -rfbport 5900 \
    -desktop "${APP_NAME:-cell}" -pointer_mode 2 -repeat -xrandr &>/dev/null &

log "VNC server ready - connect to localhost:${EXT_VNC_PORT:-5900}"
log "DISPLAY=:${DISPLAY_NUM}"

# ── xrdp (RDP gateway to existing VNC session) ────────────────────────
XRDP_BIN=$(command -v xrdp 2>/dev/null)
if [ -n "$XRDP_BIN" ]; then
    XRDP_CFG="/tmp/xrdp"
    mkdir -p "$XRDP_CFG"
    XRDP_PREFIX=$(dirname "$(dirname "$(readlink -f "$XRDP_BIN")")")

    # Copy default configs from nix store (read-only) to writable dir
    cp -a "$XRDP_PREFIX/etc/xrdp/"* "$XRDP_CFG/" 2>/dev/null || true
    chmod u+w "$XRDP_CFG/"* 2>/dev/null || true

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
    } >> "$XRDP_CFG/xrdp.ini"

    log "Starting xrdp on port 3389 (RDP → VNC :${DISPLAY_NUM})..."
    xrdp --nodaemon --config "$XRDP_CFG/xrdp.ini" >>/var/log/xrdp.log 2>&1 &

    log "xrdp ready - connect to localhost:${EXT_RDP_PORT:-3389}"
else
    log "xrdp not found — skipping RDP server"
fi
