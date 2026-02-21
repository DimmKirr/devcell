#!/bin/bash
# Wait for UTM IPSW download to complete
# Monitors temp files and VM list to detect completion
set -euo pipefail

VM_NAME="${1:-macOS}"
POLL_INTERVAL="${2:-5}"
TIMEOUT="${3:-3600}"  # Default 60 minutes

UTM_TMP="$HOME/Library/Containers/com.utmapp.UTM/Data/tmp"

echo "=== Waiting for IPSW Download ==="
echo "Looking for VM: $VM_NAME"
echo "Poll interval: ${POLL_INTERVAL}s, Timeout: ${TIMEOUT}s"
echo ""

start_time=$(date +%s)
last_status=""
tracked_file=""  # The specific temp file we're monitoring
download_finished="false"  # Once tracked file disappears, stay done

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))

    if [ "$elapsed" -ge "$TIMEOUT" ]; then
        echo ""
        echo "ERROR: Timeout waiting for download to complete"
        exit 1
    fi

    latest_tmp=""

    # Once download is finished (tracked file disappeared), don't search again
    if [ "$download_finished" = "true" ]; then
        latest_tmp=""
    elif [ -n "$tracked_file" ]; then
        # We have a tracked file - check if it still exists
        if [ -f "$tracked_file" ]; then
            latest_tmp="$tracked_file"
        else
            # Tracked file gone - download finished
            download_finished="true"
            echo ""
            echo "[INFO] Download file removed - IPSW download complete"
        fi
    else
        # No tracked file yet - find one (lock onto first found)
        for f in "$UTM_TMP"/CFNetworkDownload_*.tmp; do
            if [ -f "$f" ]; then
                tracked_file="$f"
                echo ""
                echo "[INFO] Tracking download file: $(basename "$tracked_file")"
                latest_tmp="$tracked_file"
                break
            fi
        done
    fi

    # Check if our VM exists (by exact name only)
    vm_info=$(osascript << EOF 2>/dev/null || echo "error"
tell application "UTM"
    try
        set vm to virtual machine "${VM_NAME}"
        set vmStatus to status of vm as string
        return "true|" & vmStatus & "|${VM_NAME}"
    on error
        return "false|none|"
    end try
end tell
EOF
    )

    vm_found=$(echo "$vm_info" | cut -d'|' -f1)
    vm_status=$(echo "$vm_info" | cut -d'|' -f2)
    vm_actual_name=$(echo "$vm_info" | cut -d'|' -f3)

    # Determine current state - PRIORITY: VM detection > temp file detection
    if [ "$vm_found" = "true" ]; then
        # VM exists - download complete
        status="complete"
        status_msg="VM '$VM_NAME' ready (status: $vm_status)"
    elif [ -n "$latest_tmp" ]; then
        # Tracked file exists - download in progress
        status="downloading"
        file_size_bytes=$(ls -l "$latest_tmp" 2>/dev/null | awk '{print $5}')
        file_size_human=$(ls -lh "$latest_tmp" 2>/dev/null | awk '{print $5}')

        # macOS Tahoe IPSW is ~19GB
        total_size_bytes=19000000000

        # Calculate percentage and ETA
        if [ -n "$file_size_bytes" ] && [ "$file_size_bytes" -gt 0 ]; then
            pct=$((file_size_bytes * 100 / total_size_bytes))
            eta_str="--"

            if [ "$elapsed" -ge 5 ]; then
                bytes_per_sec=$((file_size_bytes / elapsed))
                if [ "$bytes_per_sec" -gt 0 ]; then
                    remaining_bytes=$((total_size_bytes - file_size_bytes))
                    eta_secs=$((remaining_bytes / bytes_per_sec))
                    eta_min=$((eta_secs / 60))
                    eta_hr=$((eta_min / 60))
                    eta_min=$((eta_min % 60))
                    if [ "$eta_hr" -gt 0 ]; then
                        eta_str="~${eta_hr}h ${eta_min}m"
                    else
                        eta_str="~${eta_min}m"
                    fi
                fi
            fi

            status_msg="Downloading... ${file_size_human}/~19G (${pct}%) ETA: ${eta_str}"
        else
            status_msg="Downloading... 0/~19G (0%) ETA: --"
        fi
    elif [ "$last_status" = "downloading" ]; then
        # Was downloading, tracked file gone - processing/extracting
        status="processing"
        status_msg="Download complete, extracting..."
    else
        # No download yet, no VM - waiting to start
        status="waiting"
        status_msg="Waiting for download to start..."
    fi

    # Print status - single line, overwrite in place
    printf "\r[%3dm %02ds] %-60s" $((elapsed/60)) $((elapsed%60)) "$status_msg"
    last_status="$status"

    # Check for completion - exit when VM appears
    if [ "$status" = "complete" ]; then
        echo ""
        echo ""
        echo "=== Download Complete ==="
        echo "VM: $VM_NAME (status: $vm_status)"
        exit 0
    fi

    sleep "$POLL_INTERVAL"
done
