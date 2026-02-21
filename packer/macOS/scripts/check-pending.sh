#!/bin/bash
# Check for pending VM downloads in UTM
# Reports both regular VMs and any pending downloads via UI inspection
set -euo pipefail

echo "=== UTM Status Check ==="

# 0. Activate UTM and ensure window is visible
echo ""
echo "--- Activating UTM ---"
osascript << 'EOF'
tell application "UTM"
    activate
end tell
delay 1
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        -- If no windows, the main window might be minimized
        if (count of windows) = 0 then
            log "No windows visible, trying to unminimize..."
            try
                -- Try clicking the dock icon to restore
                tell application "System Events"
                    click UI element "UTM" of list 1 of application process "Dock"
                end tell
            end try
        end if
        delay 0.5
        log "Window count after activate: " & (count of windows)
    end tell
end tell
EOF

# 1. List all registered VMs via AppleScript API
echo ""
echo "--- Registered VMs (via AppleScript) ---"
osascript << 'EOF'
tell application "UTM"
    set vmList to virtual machines
    if (count of vmList) = 0 then
        log "No VMs registered"
    else
        repeat with vm in vmList
            set vmName to name of vm
            set vmStatus to status of vm
            set vmBackend to backend of vm
            log "VM: " & vmName & " | Status: " & vmStatus & " | Backend: " & vmBackend
        end repeat
    end if
end tell
EOF

# 2. Check UI for pending downloads - search ALL text elements
echo ""
echo "--- Pending Downloads (via UI) ---"
osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        set pendingFound to false

        try
            set winCount to count of windows
            if winCount = 0 then
                log "ERROR: No UTM windows open"
                return
            end if
        end try

        -- Method 1: Search entire contents for download-related text
        log "Searching all UI elements..."
        try
            set contents to entire contents of window 1
            repeat with elem in contents
                try
                    if class of elem is static text then
                        set txtVal to value of elem
                        if txtVal is not missing value and txtVal is not "" then
                            -- Check for download indicators
                            if txtVal contains "remaining" or txtVal contains "Extracting" or txtVal contains "Preparing" or txtVal contains "/s" or txtVal contains "MB of" or txtVal contains "GB of" or txtVal contains "Downloading" then
                                log "DOWNLOAD: " & txtVal
                                set pendingFound to true
                            end if
                        end if
                    else if class of elem is progress indicator then
                        try
                            set pbVal to value of elem
                            log "PROGRESS: " & (pbVal * 100) & "%"
                            set pendingFound to true
                        end try
                    end if
                end try
            end repeat
        on error errMsg
            log "entire contents error: " & errMsg
        end try

        -- Method 2: Check split groups (sidebar + main area)
        try
            set splitGroups to every splitter group of window 1
            repeat with sg in splitGroups
                set sgTexts to every static text of sg
                repeat with txt in sgTexts
                    try
                        set txtVal to value of txt
                        if txtVal contains "remaining" or txtVal contains "Downloading" then
                            log "In splitter: " & txtVal
                            set pendingFound to true
                        end if
                    end try
                end repeat
            end repeat
        end try

        -- Method 3: Look for specific pending VM UI pattern
        try
            set allGroups to every group of window 1
            log "Found " & (count of allGroups) & " groups in window"
            repeat with grp in allGroups
                -- Check for progress indicators in groups
                try
                    set grpProgress to every progress indicator of grp
                    if (count of grpProgress) > 0 then
                        log "Found progress indicator in group"
                        set pendingFound to true
                    end if
                end try
            end repeat
        end try

        if not pendingFound then
            log "No pending downloads detected in UI"
        end if
    end tell
end tell
EOF

# 3. Check file system for recent IPSW downloads
echo ""
echo "--- Recent Files (last 30 min) ---"
UTM_DATA="$HOME/Library/Containers/com.utmapp.UTM/Data"
if [ -d "$UTM_DATA" ]; then
    # Check for recently modified files that might indicate download
    find "$UTM_DATA" -type f -mmin -30 -name "*.ipsw" 2>/dev/null | head -5 || echo "No recent IPSW files"
    # Check for partial downloads
    find "$UTM_DATA" -type f -mmin -30 -name "*.download" 2>/dev/null | head -5 || true
    find "$UTM_DATA" -type f -mmin -30 -name "*.tmp" 2>/dev/null | head -5 || true
else
    echo "UTM data directory not found"
fi

# 4. Show UTM window info
echo ""
echo "--- Window Info ---"
osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        try
            set winCount to count of windows
            log "Window count: " & winCount
            if winCount > 0 then
                log "Window title: " & (title of window 1)
                -- List all static text values to debug
                log "--- All visible text ---"
                set allTexts to every static text of window 1
                repeat with txt in allTexts
                    try
                        set v to value of txt
                        if v is not missing value and v is not "" then
                            log "  " & v
                        end if
                    end try
                end repeat
            end if
        on error errMsg
            log "Error: " & errMsg
        end try
    end tell
end tell
EOF

echo ""
echo "=== End Status Check ==="
