-- Setup Assistant automation script
-- Navigates through macOS Setup Assistant using keyboard input

tell application "UTM" to activate
delay 20

tell application "System Events"
    tell process "UTM"
        set frontmost to true
        delay 0.5

        -- Screen 1: Welcome / Select Country - Press Enter to continue
        log "Screen 1: Continue"
        keystroke return
        delay 5

        -- Screen 2: Press Enter to continue
        log "Screen 2: Continue"
        keystroke return
        delay 5

        -- Screen 3: Select your country and region - Tab 3x, Space
        log "Screen 3: Country/Region"
        keystroke tab
        delay 0.3
        keystroke tab
        delay 0.3
        keystroke tab
        delay 0.3
        keystroke space
        delay 5

        -- Screen 4: Accessibility - Tab, Space
        log "Screen 4: Accessibility"
        keystroke tab
        delay 0.3
        keystroke space
        delay 5

    end tell
end tell
