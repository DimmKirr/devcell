#!/bin/bash
# Monitor UTM IPSW download progress via UI automation
# Since pending VMs are NOT exposed via AppleScript, we must use System Events
set -euo pipefail

VM_NAME="${1:-macOS}"
POLL_INTERVAL="${2:-5}"
TIMEOUT="${3:-3600}"  # Default 60 minutes

echo "=== Monitoring IPSW Download for: $VM_NAME ==="
echo "Poll interval: ${POLL_INTERVAL}s, Timeout: ${TIMEOUT}s"

start_time=$(date +%s)

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))

    if [ "$elapsed" -ge "$TIMEOUT" ]; then
        echo "ERROR: Timeout waiting for download to complete"
        exit 1
    fi

    # Check if VM now exists in the regular VM list (download complete)
    vm_exists=$(osascript << EOF
tell application "UTM"
    try
        set vm to virtual machine "${VM_NAME}"
        return "exists"
    on error
        return "not_found"
    end try
end tell
EOF
    )

    if [ "$vm_exists" = "exists" ]; then
        # Get VM status
        vm_status=$(osascript << EOF
tell application "UTM"
    set vm to virtual machine "${VM_NAME}"
    return status of vm as string
end tell
EOF
        )
        echo "SUCCESS: VM '${VM_NAME}' created! Status: $vm_status"
        exit 0
    fi

    # Check UI for pending download status
    download_status=$(osascript << 'APPLESCRIPT'
tell application "System Events"
    tell process "UTM"
        try
            -- Look for pending VM entries in the sidebar
            -- They show as list items with progress indicators
            set statusInfo to ""

            -- Check for any static text containing "remaining" (download in progress)
            set allTexts to every static text of window 1 whose value contains "remaining"
            if (count of allTexts) > 0 then
                repeat with txt in allTexts
                    set statusInfo to statusInfo & (value of txt) & "; "
                end repeat
            end if

            -- Also check for "Extracting" or "Preparing"
            set extractTexts to every static text of window 1 whose value contains "Extracting"
            if (count of extractTexts) > 0 then
                set statusInfo to "Extracting..."
            end if

            set prepTexts to every static text of window 1 whose value contains "Preparing"
            if (count of prepTexts) > 0 then
                set statusInfo to "Preparing..."
            end if

            if statusInfo is "" then
                return "no_pending"
            else
                return statusInfo
            end if
        on error errMsg
            return "error: " & errMsg
        end try
    end tell
end tell
APPLESCRIPT
    )

    # Also check download status using deeper UI inspection
    detailed_status=$(osascript << 'APPLESCRIPT'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        try
            -- Look for progress indicators
            set progressInfo to ""

            -- Check all groups in window for progress bars or download text
            repeat with grp in every group of window 1
                try
                    set grpTexts to every static text of grp
                    repeat with txt in grpTexts
                        set txtVal to value of txt
                        if txtVal contains "remaining" or txtVal contains "MB" or txtVal contains "GB" or txtVal contains "Extracting" then
                            set progressInfo to progressInfo & txtVal & " | "
                        end if
                    end repeat
                end try
            end repeat

            -- Check scroll areas (sidebar)
            repeat with sa in every scroll area of window 1
                try
                    set saTexts to every static text of sa
                    repeat with txt in saTexts
                        set txtVal to value of txt
                        if txtVal contains "remaining" or txtVal contains "Pending" then
                            set progressInfo to progressInfo & txtVal & " | "
                        end if
                    end repeat
                end try
            end repeat

            if progressInfo is "" then
                return "none"
            else
                return progressInfo
            end if
        on error errMsg
            return "error: " & errMsg
        end try
    end tell
end tell
APPLESCRIPT
    )

    # Display status
    printf "[%ds] VM not ready. " "$elapsed"
    if [ "$download_status" != "no_pending" ] && [ "$download_status" != "none" ]; then
        printf "Download: %s" "$download_status"
    fi
    if [ "$detailed_status" != "none" ] && [ -n "$detailed_status" ]; then
        printf " Detail: %s" "$detailed_status"
    fi
    printf "\n"

    sleep "$POLL_INTERVAL"
done
