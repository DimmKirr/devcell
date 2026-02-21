#!/bin/bash
set -euo pipefail

echo "=== asdf Setup ==="

# Ensure Homebrew is in PATH
eval "$(/opt/homebrew/bin/brew shellenv)"

# Install asdf via Homebrew
brew install asdf

# Configure asdf for zsh (use .zshenv for non-interactive SSH compatibility)
grep -q 'ASDF' ~/.zshenv 2>/dev/null || {
  echo 'export PATH="${ASDF_DATA_DIR:-$HOME/.asdf}/shims:$PATH"' >> ~/.zshenv
}

# Configure asdf to use .tool-versions files
echo "legacy_version_file = yes" > ~/.asdfrc

# Add asdf to current session
export PATH="${ASDF_DATA_DIR:-$HOME/.asdf}/shims:$(brew --prefix asdf)/libexec:$PATH"

# Install asdf plugins
asdf plugin add nodejs https://github.com/asdf-vm/asdf-nodejs.git || true
asdf plugin add golang https://github.com/asdf-community/asdf-golang.git || true
asdf plugin add python https://github.com/asdf-community/asdf-python.git || true
asdf plugin add ruby https://github.com/asdf-vm/asdf-ruby.git || true
asdf plugin add terraform https://github.com/asdf-community/asdf-hashicorp.git || true
asdf plugin add opentofu https://github.com/virtualroot/asdf-opentofu.git || true
asdf plugin add vagrant https://github.com/asdf-community/asdf-hashicorp.git || true
asdf plugin add packer https://github.com/asdf-community/asdf-hashicorp.git || true
asdf plugin add uv https://github.com/asdf-community/asdf-uv.git || true

echo "=== asdf setup complete ==="
