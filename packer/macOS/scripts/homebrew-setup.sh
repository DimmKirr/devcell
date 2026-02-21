#!/bin/bash
set -euo pipefail

echo "=== Homebrew Setup ==="

# Install Homebrew
if ! command -v brew &>/dev/null; then
  echo "Installing Homebrew..."
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" </dev/null
fi

# Add Homebrew to PATH for this session
eval "$(/opt/homebrew/bin/brew shellenv)"

# Add to .zshenv for future sessions (works with non-interactive SSH)
grep -q 'brew shellenv' ~/.zshenv 2>/dev/null || {
  echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zshenv
}

# Install essential packages
brew install \
  coreutils \
  git \
  bash \
  jq \
  xz \
  openssl \
  readline

# Install Task (Taskfile)
mkdir -p ~/.local/bin
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin
grep -q '.local/bin' ~/.zshenv 2>/dev/null || echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshenv

echo "=== Homebrew setup complete ==="
