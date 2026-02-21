#!/bin/bash
set -euo pipefail

echo "=== Vagrant User Setup ==="

# Base VM already has vagrant:vagrant user
# This script configures SSH keys and sudo

USERNAME="vagrant"

# Configure SSH directory
mkdir -p ~/.ssh
chmod 700 ~/.ssh

# Add Vagrant insecure public key for passwordless SSH
curl -fsSL https://raw.githubusercontent.com/hashicorp/vagrant/main/keys/vagrant.pub >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys

# Configure SSH server (if not already configured)
if ! grep -q "Vagrant SSH config" /etc/ssh/sshd_config 2>/dev/null; then
  sudo tee -a /etc/ssh/sshd_config > /dev/null << 'EOF'

# Vagrant SSH config
PubkeyAuthentication yes
PasswordAuthentication yes
PermitEmptyPasswords no
ChallengeResponseAuthentication no
UsePAM yes
EOF
fi

# Enable passwordless sudo for vagrant user
echo "$USERNAME ALL=(ALL) NOPASSWD: ALL" | sudo tee /etc/sudoers.d/$USERNAME > /dev/null
sudo chmod 440 /etc/sudoers.d/$USERNAME

# Set hostname for mDNS discovery (vagrant-macos.local)
sudo scutil --set ComputerName "vagrant-macos"
sudo scutil --set LocalHostName "vagrant-macos"
sudo scutil --set HostName "vagrant-macos.local"

# Restart mDNSResponder to pick up hostname change
sudo killall -HUP mDNSResponder 2>/dev/null || true

# Restart SSH
sudo launchctl stop com.openssh.sshd 2>/dev/null || true
sudo launchctl start com.openssh.sshd 2>/dev/null || true

echo "=== Vagrant user setup complete ==="
echo "SSH: vagrant@vagrant-macos.local (mDNS)"
