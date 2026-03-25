#!/bin/bash
# 05-shell-rc.sh — shell rc files + nix profile for session user
# Sourced by entrypoint.sh if present and executable.

# ── Nix profile symlink ──────────────────────────────────────────────
# nix.sh resolves $HOME/.nix-profile to find nix tools. Symlink so the
# session user gets nix in PATH without running home-manager.
ln -sfT "$(readlink -f /opt/devcell/.nix-profile)" "$HOME/.nix-profile"

# ── Shell rc files — source from /opt/devcell (always current) ───────
# /opt/devcell/.zshenv etc. are home-manager symlinks → nix store.
# Sourcing the stable path means hm-session-vars.sh (with LD_LIBRARY_PATH,
# GOPATH, etc.) always reflects the current generation — no stale hashes.
for file in .bashrc .zshrc .zshenv .profile; do
    [ -f "$DEVCELL_HOME/$file" ] || continue
    cat > "$HOME/$file" <<RCEOF
. "$DEVCELL_HOME/$file"
# -- devcell session user overrides --
export USER="$HOST_USER"
export GOPATH="$HOME/go"
export MISE_DATA_DIR="$HOME/.local/share/mise"
export MISE_GLOBAL_CONFIG_FILE="$(readlink -f "$DEVCELL_HOME/.config/mise/config.toml" 2>/dev/null)"
export MISE_NODE_DEFAULT_PACKAGES_FILE="$(readlink -f "$DEVCELL_HOME/.default-npm-packages" 2>/dev/null)"
export NIX_CONF_DIR="/opt/devcell/.config/nix"
export STARSHIP_CONFIG="/opt/devcell/.config/starship.toml"
export FONTCONFIG_PATH="/opt/devcell/.config/fontconfig"
export HISTFILE="$HOME/.zsh_history"
export PATH="$HOME/go/bin:/opt/devcell/.local/state/nix/profiles/profile/bin:$HOME/.local/share/mise/shims:/opt/python-tools/.venv/bin:/opt/npm-tools/node_modules/.bin\${PATH:+:}\${PATH}"
# LD_LIBRARY_PATH from full nix profile closure (docker exec sessions don't
# inherit PID 1's env, so each shell must source the file independently).
_NLD="/opt/devcell/.nix-ld-library-path"
if [ -f "\$_NLD" ]; then
  export LD_LIBRARY_PATH="\$(cat "\$_NLD")\${LD_LIBRARY_PATH:+:}\${LD_LIBRARY_PATH}"
fi
RCEOF
    chown "$HOST_USER" "$HOME/$file"
done
