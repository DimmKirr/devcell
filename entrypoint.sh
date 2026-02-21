#!/bin/bash
# Entrypoint script - initializes home directory and optionally starts VNC server

# ============================================================
# Initialize home directory files (needed when ~ is mounted)
# Copy templates from multiple sources to $HOME if they don't exist
# ============================================================

OPT_HOME="/opt/home"
REPO_HOMEDIR="${WORKSPACE}/homedir"

# Function to copy a file or directory if source exists and target doesn't
copy_if_not_exists() {
    local src="$1"
    local dest="$2"

    if [ -e "$src" ] && [ ! -e "$dest" ]; then
        echo "Copying $(basename "$src") to $dest"
        cp -r "$src" "$dest"
        return 0
    fi
    return 1
}

# Function to merge Claude settings.json using jq
merge_claude_settings() {
    local template_file="$1"
    local target_file="$2"

    if [ ! -f "$template_file" ]; then
        return 1
    fi

    # Create .claude directory if it doesn't exist
    mkdir -p "$(dirname "$target_file")"

    # If target doesn't exist, just copy the template
    if [ ! -f "$target_file" ]; then
        echo "Creating Claude settings from template"
        cp "$template_file" "$target_file"
        return 0
    fi

    # Target exists - create backup before merge
    local backup_file="${target_file}.backup-$(date +%Y%m%d-%H%M%S)"
    cp "$target_file" "$backup_file"
    echo "✓ Created backup: $(basename "$backup_file")"

    # Keep only last 5 backups
    ls -t "${target_file}.backup-"* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true

    # Target exists - smart merge using jq
    echo "Merging Claude settings (preserving existing configuration)"

    # Create temp file for merged settings
    local temp_file=$(mktemp)

    # Smart merge strategy:
    # - If user already has PermissionRequest hooks, keep them (don't overwrite)
    # - If user doesn't have PermissionRequest hooks, add ours from template
    # - All other settings are merged (template adds missing keys)
    jq -s '
      if .[0].hooks.PermissionRequest then
        .[0]
      else
        .[0] * .[1]
      end
    ' "$target_file" "$template_file" > "$temp_file" 2>/dev/null

    # Only update if merge was successful
    if [ $? -eq 0 ] && [ -s "$temp_file" ]; then
        # Validate merged JSON before replacing
        if jq empty "$temp_file" 2>/dev/null; then
            mv "$temp_file" "$target_file"
            if grep -q "PermissionRequest" "$target_file"; then
                echo "✓ Claude settings updated (PermissionRequest hook configured)"
            else
                echo "✓ Claude settings preserved (custom PermissionRequest hook detected)"
            fi
        else
            echo "⚠ Merged settings invalid, restoring from backup"
            cp "$backup_file" "$target_file"
            rm -f "$temp_file"
        fi
    else
        echo "⚠ Failed to merge Claude settings, restoring from backup"
        cp "$backup_file" "$target_file"
        rm -f "$temp_file"
    fi
}

# ============================================================
# First: Copy from repo's homedir/ (if it exists)
# This allows per-repo customization and takes precedence
# ============================================================

if [ -d "$REPO_HOMEDIR" ]; then
    echo "Found repo homedir at $REPO_HOMEDIR"

    # Special handling for .claude directory (merge settings, copy hooks)
    if [ -d "$REPO_HOMEDIR/.claude" ]; then
        echo "Setting up Claude configuration..."

        # Copy/update hooks directory (safe to overwrite)
        if [ -d "$REPO_HOMEDIR/.claude/hooks" ]; then
            mkdir -p "$HOME/.claude/hooks"
            echo "Copying Claude hooks..."
            cp -r "$REPO_HOMEDIR/.claude/hooks/"* "$HOME/.claude/hooks/" 2>/dev/null || true
            # Make all hook scripts executable
            find "$HOME/.claude/hooks" -type f -name "*.sh" -exec chmod +x {} \; 2>/dev/null || true
        fi

        # Merge settings.json (preserve existing configuration)
        if [ -f "$REPO_HOMEDIR/.claude/settings.json" ]; then
            merge_claude_settings "$REPO_HOMEDIR/.claude/settings.json" "$HOME/.claude/settings.json"
        fi
    fi

    # Copy all other files and directories from repo homedir
    # Skip files that already exist to avoid overwriting user customizations
    find "$REPO_HOMEDIR" -mindepth 1 -maxdepth 1 | while read -r item; do
        basename_item=$(basename "$item")

        # Skip .claude (already handled above)
        if [ "$basename_item" = ".claude" ]; then
            continue
        fi

        dest="$HOME/$basename_item"

        if [ ! -e "$dest" ]; then
            echo "Copying $basename_item from repo to ~/"
            cp -r "$item" "$dest"
        fi
    done
fi

# ============================================================
# Second: Copy from /opt/home (image defaults, backward compatibility)
# ============================================================

# Copy individual dotfiles from /opt/home if they don't exist in $HOME
for file in .asdfrc .bashrc .profile .zshrc .tool-versions opencode.json; do
    copy_if_not_exists "$OPT_HOME/$file" "$HOME/$file"
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
