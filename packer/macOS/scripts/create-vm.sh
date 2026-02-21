#!/bin/bash
# Create macOS VM via UTM - full wizard automation
set -euo pipefail

VM_NAME="${1:-macos-base}"
MEMORY="${2:-8192}"
CPUS="${3:-4}"
DISK_MB="${4:-65536}"

open -a UTM
sleep 2

# Click File > New from menu bar
echo "=== Opening File > New ==="
osascript << 'EOF'
tell application "UTM" to activate
delay 1
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        click menu item "New…" of menu "File" of menu bar 1
    end tell
end tell
EOF

sleep 3

# Screen 1: Virtualize vs Emulate (Start)
echo "=== Screen 1: Virtualize/Emulate ==="

# DEBUG
echo "=== DEBUG UI DUMP (Screen 1) ==="
./scripts/debug-ui.sh
echo "=== END DEBUG ==="

osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 1
        repeat 10 times
            if exists sheet 1 of window 1 then exit repeat
            delay 0.5
        end repeat
        delay 0.5

        -- Click Virtualize (row 2) - auto-advances to OS Selection
        log "Clicking Virtualize (row 2)"
        click button 1 of UI element 1 of row 2 of outline 1 of scroll area 1 of group 1 of sheet 1 of window 1
    end tell
end tell
EOF

sleep 1

# Screen 2: OS Selection
echo "=== Screen 2: OS Selection ==="
sleep 1
osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 1

        -- Wait for sheet to be ready
        repeat 10 times
            try
                if exists sheet 1 of window 1 then exit repeat
            end try
            delay 0.5
        end repeat

        -- Click macOS 12+ (row 2) - auto-advances to Hardware
        log "Clicking macOS 12+ (row 2)"
        click button 1 of UI element 1 of row 2 of outline 1 of scroll area 1 of group 1 of sheet 1 of window 1
    end tell
end tell
EOF

sleep 1

# Screen 3: Hardware
# Use keyboard input for SwiftUI TextField bindings (same fix as VM name)
echo "=== Screen 3: Hardware ==="
osascript << EOF
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 1

        -- Wait for Hardware screen to fully load
        repeat 20 times
            try
                if exists text field 1 of UI element 1 of row 2 of outline 1 of scroll area 1 of group 1 of sheet 1 of window 1 then
                    log "Hardware screen loaded"
                    exit repeat
                end if
            end try
            delay 0.5
        end repeat
        delay 0.5

        -- Set memory via clipboard (avoids keyboard layout issues)
        log "Setting memory to ${MEMORY}"
        set memField to text field 1 of UI element 1 of row 2 of outline 1 of scroll area 1 of group 1 of sheet 1 of window 1
        set the clipboard to "${MEMORY}"
        set focused of memField to true
        delay 0.3
        set frontmost to true
        delay 0.2
        keystroke "a" using command down
        delay 0.2
        keystroke "v" using command down
        delay 0.3

        -- Set CPU cores via clipboard
        log "Setting CPU cores to ${CPUS}"
        set cpuField to text field 1 of UI element 1 of row 4 of outline 1 of scroll area 1 of group 1 of sheet 1 of window 1
        set the clipboard to "${CPUS}"
        set focused of cpuField to true
        delay 0.3
        set frontmost to true
        delay 0.2
        keystroke "a" using command down
        delay 0.2
        keystroke "v" using command down
        delay 0.3

        -- Tab to commit the last field
        set frontmost to true
        delay 0.2
        keystroke (ASCII character 9)
        delay 0.3

        -- Click Continue (button 3 of group 1 of sheet 1)
        -- Group 1 has: button 1=Cancel, button 2=Go Back, button 3=Continue
        set sheetBtnCount to count of buttons of group 1 of sheet 1 of window 1
        log "Sheet group 1 has " & sheetBtnCount & " buttons"
        log "Clicking button " & sheetBtnCount & " (Continue) of group 1 of sheet 1"
        click button sheetBtnCount of group 1 of sheet 1 of window 1
    end tell
end tell
EOF

sleep 1

# Screen 4: macOS IPSW ("macOS")
echo "=== Screen 4: macOS IPSW ==="
osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 1

        log "On IPSW screen, clicking Continue..."
        set sheetBtnCount to count of buttons of group 1 of sheet 1 of window 1
        log "Sheet group 1 has " & sheetBtnCount & " buttons"
        log "Clicking button " & sheetBtnCount & " (Continue)"
        click button sheetBtnCount of group 1 of sheet 1 of window 1
    end tell
end tell
EOF

# CRITICAL: Wait for async fetchLatestPlatform() to complete
# When Continue is clicked, UTM contacts Apple to get the IPSW URL asynchronously.
# If we proceed too fast, macRecoveryIpswURL will be nil and VM won't have IPSW linked.
echo "Waiting for IPSW URL fetch to complete (5s)..."
sleep 5

echo "Proceeding to Storage screen..."

sleep 1

# Screen 5: Storage
echo "=== Screen 5: Storage ==="
osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true

        -- Wait for Storage screen (may take time if downloading IPSW)
        log "Waiting for Storage screen and clicking Continue..."
        repeat 900 times -- wait up to 15 min for download
            delay 1
            try
                set sheetBtnCount to count of buttons of group 1 of sheet 1 of window 1
                if sheetBtnCount >= 3 then
                    log "Found " & sheetBtnCount & " buttons in sheet group, clicking button " & sheetBtnCount
                    click button sheetBtnCount of group 1 of sheet 1 of window 1
                    exit repeat
                end if
            end try
        end repeat
    end tell
end tell
EOF

sleep 1

# Screen 6: Summary
echo "=== Screen 6: Summary ==="

# DEBUG: dump Summary screen
echo "=== DEBUG UI DUMP (Summary) ==="
./scripts/debug-ui.sh
echo "=== END DEBUG ==="

osascript << EOF
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 1

        -- Find the Name text field - it's the ONLY enabled text field on Summary screen
        -- SwiftUI Form is inside ScrollView, so check scroll areas in group 1
        log "Looking for Name text field (the only enabled one)..."

        set nameFieldFound to false
        set nameField to missing value

        -- Method 1: Check scroll area in group 1 of sheet
        try
            set scrollAreas to every scroll area of group 1 of sheet 1 of window 1
            log "Found " & (count of scrollAreas) & " scroll areas in group 1"
            repeat with sa in scrollAreas
                set saFields to every text field of sa
                log "Scroll area has " & (count of saFields) & " text fields"
                repeat with fld in saFields
                    if enabled of fld then
                        set nameField to fld
                        set nameFieldFound to true
                        log "Found enabled text field with value: " & (value of fld)
                        exit repeat
                    end if
                end repeat
                if nameFieldFound then exit repeat
            end repeat
        on error errMsg
            log "Method 1 error: " & errMsg
        end try

        -- Method 2: Use entire contents to find any enabled text field
        if not nameFieldFound then
            log "Trying entire contents method..."
            try
                set contents to entire contents of sheet 1 of window 1
                repeat with elem in contents
                    try
                        if class of elem is text field then
                            if enabled of elem then
                                set nameField to elem
                                set nameFieldFound to true
                                log "Found enabled text field via entire contents: " & (value of elem)
                                exit repeat
                            end if
                        end if
                    end try
                end repeat
            on error errMsg
                log "Method 2 error: " & errMsg
            end try
        end if

        -- Set the Name field and Save via keyboard input
        -- 1. Type VM name
        -- 2. Press Return to save
        log "Setting VM name to '${VM_NAME}' and saving..."

        -- Use clipboard to avoid keyboard layout issues
        set the clipboard to "${VM_NAME}"

        -- Ensure focus before keystrokes
        set frontmost to true
        delay 0.3

        if nameFieldFound then
            set focused of nameField to true
            delay 0.3
            -- Re-focus window before keystrokes
            set frontmost to true
            delay 0.2
            keystroke "a" using command down  -- Select all
            delay 0.2
            keystroke "v" using command down  -- Paste from clipboard
        else
            -- Fallback: Tab to first field and paste
            log "Name field not found, using Tab fallback"
            set frontmost to true
            delay 0.2
            keystroke (ASCII character 9) -- Tab
            delay 0.3
            keystroke "a" using command down
            delay 0.2
            keystroke "v" using command down  -- Paste from clipboard
        end if

        -- Tab to commit the text field, then wait
        log "Pressing Tab to commit name..."
        delay 0.3
        set frontmost to true
        delay 0.2
        keystroke (ASCII character 9)
        delay 1

        -- Press Return to save
        log "Pressing Return to save..."
        set frontmost to true
        delay 0.2
        keystroke return
        log "Save triggered!"
    end tell
end tell
EOF

sleep 2

# Verify VM was created with correct name
echo "=== Verifying VM creation ==="

VM_EXISTS=$(osascript << EOF
tell application "UTM"
    try
        set vm to virtual machine "${VM_NAME}"
        return "found"
    on error
        return "not_found"
    end try
end tell
EOF
)

if [ "$VM_EXISTS" = "found" ]; then
    echo "SUCCESS: VM '${VM_NAME}' created!"
else
    echo "ERROR: VM '${VM_NAME}' not found after creation"
    echo "Available VMs:"
    osascript -e 'tell application "UTM" to get name of every virtual machine'
    exit 1
fi

# Final verification
echo "=== Final VM Status ==="
osascript << EOF
tell application "UTM"
    set vmList to virtual machines
    repeat with vm in vmList
        set vmName to name of vm
        set vmStatus to status of vm
        log "VM: " & vmName & " | Status: " & vmStatus
    end repeat
end tell
EOF

echo "VM created! Monitoring installation..."

# Monitor VM status
echo "=== Monitoring VM Status ==="
osascript << EOF
tell application "UTM"
    try
        set vm to virtual machine "${VM_NAME}"
        set vmStatus to status of vm
        log "VM '${VM_NAME}' status: " & vmStatus
    on error
        log "VM '${VM_NAME}' not found"
    end try
end tell
EOF
