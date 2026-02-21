#!/bin/bash
# Debug UTM Summary screen to find Name text field path
# Run this while on the Summary screen of the wizard

osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        set frontmost to true
        log "============================================"
        log "SUMMARY SCREEN TEXT FIELD SEARCH"
        log "============================================"

        -- Check sheet exists
        try
            set s to sheet 1 of window 1
            log "Sheet 1 exists"
        on error
            log "NO SHEET - wizard may not be open"
            return
        end try

        -- Find ALL text fields anywhere in the sheet
        log ""
        log "=== ALL TEXT FIELDS IN SHEET ==="
        try
            set allFields to every text field of sheet 1 of window 1
            log "Found " & (count of allFields) & " text fields directly in sheet"
            repeat with i from 1 to count of allFields
                set fld to item i of allFields
                try
                    log "  [" & i & "] value: " & (value of fld)
                end try
            end repeat
        on error errMsg
            log "Error getting direct fields: " & errMsg
        end try

        -- Check text fields in groups
        log ""
        log "=== TEXT FIELDS IN GROUPS ==="
        set grpCount to count of groups of sheet 1 of window 1
        repeat with g from 1 to grpCount
            set grp to group g of sheet 1 of window 1
            try
                set grpFields to every text field of grp
                if (count of grpFields) > 0 then
                    log "Group " & g & " has " & (count of grpFields) & " text fields:"
                    repeat with f from 1 to count of grpFields
                        set fld to item f of grpFields
                        try
                            log "  [" & f & "] value: " & (value of fld) & " | enabled: " & (enabled of fld)
                        end try
                    end repeat
                end if
            end try
        end repeat

        -- Check scroll areas (Form is inside ScrollView)
        log ""
        log "=== TEXT FIELDS IN SCROLL AREAS ==="
        try
            set scrollAreas to every scroll area of sheet 1 of window 1
            log "Found " & (count of scrollAreas) & " scroll areas"
            repeat with sa from 1 to count of scrollAreas
                set saFields to every text field of scroll area sa of sheet 1 of window 1
                if (count of saFields) > 0 then
                    log "Scroll area " & sa & " has " & (count of saFields) & " text fields:"
                    repeat with f from 1 to count of saFields
                        set fld to item f of saFields
                        try
                            log "  [" & f & "] value: " & (value of fld) & " | enabled: " & (enabled of fld)
                        end try
                    end repeat
                end if
            end repeat
        on error errMsg
            log "Error with scroll areas: " & errMsg
        end try

        -- Check groups inside groups
        log ""
        log "=== NESTED STRUCTURE ==="
        repeat with g from 1 to grpCount
            set grp to group g of sheet 1 of window 1
            try
                set nestedGroups to every group of grp
                if (count of nestedGroups) > 0 then
                    log "Group " & g & " has " & (count of nestedGroups) & " nested groups"
                    repeat with ng from 1 to count of nestedGroups
                        set ngrp to group ng of grp
                        try
                            set ngFields to every text field of ngrp
                            if (count of ngFields) > 0 then
                                log "  Nested group " & ng & " has " & (count of ngFields) & " text fields:"
                                repeat with f from 1 to count of ngFields
                                    try
                                        log "    [" & f & "] value: " & (value of item f of ngFields)
                                    end try
                                end repeat
                            end if
                        end try
                    end repeat
                end if
            end try

            -- Check scroll areas in groups
            try
                set grpScrolls to every scroll area of grp
                if (count of grpScrolls) > 0 then
                    log "Group " & g & " has " & (count of grpScrolls) & " scroll areas"
                    repeat with gs from 1 to count of grpScrolls
                        set gsa to scroll area gs of grp
                        try
                            set gsaFields to every text field of gsa
                            if (count of gsaFields) > 0 then
                                log "  Scroll area " & gs & " has " & (count of gsaFields) & " text fields:"
                                repeat with f from 1 to count of gsaFields
                                    try
                                        set fld to item f of gsaFields
                                        log "    [" & f & "] value: " & (value of fld) & " | enabled: " & (enabled of fld)
                                    end try
                                end repeat
                            end if
                        end try
                    end repeat
                end if
            end try
        end repeat

        -- Brute force: entire contents
        log ""
        log "=== ALL TEXT FIELDS (entire contents) ==="
        try
            set contents to entire contents of sheet 1 of window 1
            set fieldCount to 0
            repeat with elem in contents
                try
                    if class of elem is text field then
                        set fieldCount to fieldCount + 1
                        log "[Field " & fieldCount & "] value: " & (value of elem) & " | enabled: " & (enabled of elem)
                        -- Try to get the full path
                        try
                            log "  Path hint: " & (description of elem)
                        end try
                    end if
                end try
            end repeat
            log "Total text fields found: " & fieldCount
        on error errMsg
            log "Error with entire contents: " & errMsg
        end try

        log ""
        log "============================================"
        log "END SUMMARY DEBUG"
        log "============================================"
    end tell
end tell
EOF
