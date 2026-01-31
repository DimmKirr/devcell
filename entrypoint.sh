#!/bin/bash
# Entrypoint script - initializes home directory and optionally starts VNC server

# ============================================================
# Initialize home directory files (needed when ~ is mounted)
# Copy templates from /opt/home to $HOME if they don't exist
# ============================================================

OPT_HOME="/opt/home"

# Copy individual dotfiles from /opt/home if they don't exist in $HOME
for file in .asdfrc .bashrc .profile .zshrc .tool-versions; do
    if [ -f "$OPT_HOME/$file" ] && [ ! -f "$HOME/$file" ]; then
        echo "Copying $file to ~/"
        cp "$OPT_HOME/$file" "$HOME/$file"
    fi
done

# Copy .config directory structure (nix config, etc.)
if [ -d "$OPT_HOME/.config" ]; then
    mkdir -p "$HOME/.config"
    # Copy nix config if it exists and target doesn't
    if [ -d "$OPT_HOME/.config/nix" ] && [ ! -d "$HOME/.config/nix" ]; then
        echo "Copying .config/nix/ to ~/"
        cp -r "$OPT_HOME/.config/nix" "$HOME/.config/"
    fi
fi

# Create .local/bin if it doesn't exist
mkdir -p "$HOME/.local/bin"

# ============================================================
# GUI Setup (optional)
# ============================================================

# Only start GUI if DEVCELL_GUI_ENABLED is set to "true"
if [ "$DEVCELL_GUI_ENABLED" = "true" ]; then
    DISPLAY_NUM=99
    RESOLUTION=1920x1080x24

    # Create X11 socket directory (needed for non-root user)
    sudo mkdir -p /tmp/.X11-unix
    sudo chmod 1777 /tmp/.X11-unix

    # Start Xvfb (X virtual framebuffer)
    echo "Starting Xvfb on display :${DISPLAY_NUM}..."
    Xvfb :${DISPLAY_NUM} -screen 0 ${RESOLUTION} &
    sleep 1

    # Export DISPLAY
    export DISPLAY=:${DISPLAY_NUM}

    # Start fluxbox window manager
    echo "Starting fluxbox..."
    fluxbox &>/dev/null &
    sleep 1

    # Start x11vnc
    echo "Starting x11vnc on port 5900..."
    x11vnc -display :${DISPLAY_NUM} -forever -shared -passwd vnc -rfbport 5900 &>/dev/null &

    echo "VNC server ready - connect to localhost:${EXT_VNC_PORT:-5900}"
    echo "DISPLAY=:${DISPLAY_NUM}"
else
    echo "GUI disabled (DEVCELL_GUI_ENABLED != true)"
fi

# Execute the command passed to the container (or default to keeping alive)
exec "$@"
