#!/bin/bash
set -euo pipefail

echo "=== Cleanup ==="

# Clear Homebrew cache
brew cleanup -s 2>/dev/null || true
rm -rf "$(brew --cache)" 2>/dev/null || true

# Clear npm cache if npm exists
command -v npm &>/dev/null && npm cache clean --force 2>/dev/null || true

# Clear pip cache if pip exists
rm -rf ~/.cache/pip 2>/dev/null || true

# Clear general caches
rm -rf ~/.cache/* 2>/dev/null || true

# Clear bash/zsh history
rm -f ~/.bash_history ~/.zsh_history 2>/dev/null || true
history -c 2>/dev/null || true

# Remove temporary files
sudo rm -rf /tmp/* 2>/dev/null || true
sudo rm -rf /var/tmp/* 2>/dev/null || true

# Clear system logs (optional, reduces image size)
sudo rm -rf /var/log/*.log 2>/dev/null || true
sudo rm -rf /var/log/*.gz 2>/dev/null || true

# Clear DNS cache
sudo dscacheutil -flushcache 2>/dev/null || true

# Compact disk (for VM)
echo "Zeroing free space for better compression..."
cat /dev/zero > /tmp/zero.fill 2>/dev/null || true
rm -f /tmp/zero.fill

echo "=== Cleanup complete ==="
