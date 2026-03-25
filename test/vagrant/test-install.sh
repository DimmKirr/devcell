#!/usr/bin/env bash
# E2E install test: validates full new-user pipeline on a clean Linux VM.
#
# 1. Install Homebrew (Linuxbrew)
# 2. Install devcell via Homebrew cask
# 3. Run `cell claude --version` and assert version output
#
# Parameterized via CELL_VERSION env var (default: 0.0.0).
set -euo pipefail

CELL_VERSION="${CELL_VERSION:-0.0.0}"

echo "=== E2E Install Test (cell v${CELL_VERSION}) ==="

# ---------- 1. Ensure Docker is available ----------
if ! command -v docker &>/dev/null; then
  echo "--- Installing Docker ---"
  sudo apt-get update -qq
  sudo apt-get install -y -qq ca-certificates curl gnupg
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/debian/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  sudo chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
    sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
  sudo apt-get update -qq
  sudo apt-get install -y -qq docker-ce docker-ce-cli containerd.io
  sudo usermod -aG docker vagrant
fi

echo "Docker: $(docker --version)"

# ---------- 2. Install Homebrew ----------
if ! command -v brew &>/dev/null; then
  echo "--- Installing Homebrew ---"
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  # Add to PATH for this session
  eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
fi

echo "Homebrew: $(brew --version | head -1)"

# ---------- 3. Install devcell ----------
echo "--- Installing devcell (cask devcell-dev@${CELL_VERSION}) ---"
brew install --cask "DimmKirr/tap/devcell-dev@${CELL_VERSION}"

CELL_BIN="$(brew --prefix)/bin/cell"
if [ ! -x "$CELL_BIN" ]; then
  echo "FAIL: cell binary not found at $CELL_BIN"
  exit 1
fi

echo "cell binary: $CELL_BIN"

# ---------- 4. Run cell claude --version ----------
echo "--- Running: cell claude --version ---"

# cell prints version to stderr; capture both streams
OUTPUT=$("$CELL_BIN" claude --version 2>&1) || true

echo "$OUTPUT"

# ---------- 5. Assert version output ----------
# Expected pattern: "cell v0.0.0-..." or "cell 0.0.0-..."
if echo "$OUTPUT" | grep -qE "cell\s+v?[0-9]+\.[0-9]+\.[0-9]+"; then
  echo ""
  echo "=== PASS: cell claude --version returned a valid version ==="
else
  echo ""
  echo "=== FAIL: version string not found in output ==="
  exit 1
fi
