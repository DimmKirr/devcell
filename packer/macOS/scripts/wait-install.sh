#!/bin/bash
# Wait for macOS installation to complete
# Monitors disk size growth and VM status
set -euo pipefail

VM_NAME="${1:-macos-base}"
POLL_INTERVAL="${2:-10}"
TIMEOUT="${3:-3600}"  # Default 60 minutes

UTM_DATA="$HOME/Library/Containers/com.utmapp.UTM/Data/Documents/${VM_NAME}.utm/Data"

echo "=== Waiting for macOS Installation ==="
echo "VM: $VM_NAME"
echo "Poll interval: ${POLL_INTERVAL}s, Timeout: ${TIMEOUT}s"
echo ""

start_time=$(date +%s)
last_size=""

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))

    if [ "$elapsed" -ge "$TIMEOUT" ]; then
        echo ""
        echo "ERROR: Timeout waiting for installation to complete"
        exit 1
    fi

    # Get VM status
    vm_status=$(osascript -e "tell application \"UTM\" to get status of virtual machine \"${VM_NAME}\"" 2>/dev/null || echo "unknown")

    # Get disk size
    disk_size=$(ls -lh "$UTM_DATA"/*.img 2>/dev/null | awk '{print $5}' | head -1 || echo "?")
    disk_bytes=$(ls -l "$UTM_DATA"/*.img 2>/dev/null | awk '{print $5}' | head -1 || echo "0")

    # Estimate progress (macOS install is typically 15-20GB)
    if [ "$disk_bytes" -gt 0 ] 2>/dev/null; then
        # Assume 18GB target for rough percentage
        pct=$((disk_bytes * 100 / 18000000000))
        [ "$pct" -gt 100 ] && pct=100
        progress="${pct}%"
    else
        progress="?"
    fi

    # Print status
    printf "\r[%3dm %02ds] Status: %-10s | Disk: %8s (~%s)          " \
        $((elapsed/60)) $((elapsed%60)) "$vm_status" "$disk_size" "$progress"

    # Check if installation might be complete
    # - VM is stopped (rebooted after install)
    # - Or disk is large enough (>15GB) and VM is still running (Setup Assistant)
    if [ "$vm_status" = "stopped" ] && [ "$disk_bytes" -gt 10000000000 ] 2>/dev/null; then
        echo ""
        echo ""
        echo "=== VM stopped after installation ==="
        echo "Disk size: $disk_size"
        echo "The VM may need to be started again for Setup Assistant"
        exit 0
    fi

    # If disk is large and status changes from last check, note it
    if [ "$disk_size" != "$last_size" ] && [ "$disk_bytes" -gt 15000000000 ] 2>/dev/null; then
        echo ""
        echo "[INFO] Disk now $disk_size - installation likely complete or near complete"
    fi
    last_size="$disk_size"

    sleep "$POLL_INTERVAL"
done
