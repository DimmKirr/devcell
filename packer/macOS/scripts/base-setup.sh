#!/bin/bash
set -euo pipefail

echo "=== Base macOS Setup ==="

# Enable SSH (Remote Login) - should already be enabled in base VM
sudo systemsetup -setremotelogin on 2>/dev/null || true

# Disable screen saver and sleep for unattended operation
sudo pmset -a displaysleep 0
sudo pmset -a sleep 0
sudo pmset -a disksleep 0

# Disable Gatekeeper for easier app installation
sudo spctl --master-disable 2>/dev/null || true

# Accept Xcode license if Xcode is installed
if [ -d "/Applications/Xcode.app" ]; then
  sudo xcodebuild -license accept 2>/dev/null || true
fi

# Install Xcode Command Line Tools if not present
if ! xcode-select -p &>/dev/null; then
  echo "Installing Xcode Command Line Tools..."
  touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
  softwareupdate -i -a --agree-to-license 2>/dev/null || true
  rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress

  # Alternative method if softwareupdate doesn't work
  xcode-select --install 2>/dev/null || true
fi

echo "=== Base setup complete ==="
