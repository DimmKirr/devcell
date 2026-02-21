#!/bin/bash
# Debug UTM UI - dump all accessible elements

osascript << 'EOF'
tell application "System Events"
    tell process "UTM"
        log "============================================"
        log "UTM UI DEBUG DUMP"
        log "============================================"

        -- Window info
        log ""
        log "=== WINDOW 1 ==="
        try
            set w to window 1
            log "  name: " & (name of w)
            log "  title: " & (title of w)
            log "  role: " & (role of w)
            log "  subrole: " & (subrole of w)
        end try

        -- Window buttons
        log ""
        log "=== WINDOW BUTTONS ==="
        set winBtns to every button of window 1
        repeat with i from 1 to count of winBtns
            set btn to item i of winBtns
            log "  [Button " & i & "]"
            try
                log "    name: " & (name of btn)
            end try
            try
                log "    title: " & (title of btn)
            end try
            try
                log "    description: " & (description of btn)
            end try
            try
                log "    role: " & (role of btn)
            end try
            try
                log "    subrole: " & (subrole of btn)
            end try
            try
                log "    value: " & (value of btn)
            end try
            try
                log "    help: " & (help of btn)
            end try
            try
                log "    enabled: " & (enabled of btn)
            end try
            try
                log "    focused: " & (focused of btn)
            end try
            try
                log "    position: " & (item 1 of (position of btn)) & "," & (item 2 of (position of btn))
            end try
            try
                log "    size: " & (item 1 of (size of btn)) & "x" & (item 2 of (size of btn))
            end try
            try
                log "    identifier: " & (identifier of btn)
            end try
        end repeat

        -- Sheet info
        log ""
        log "=== SHEET 1 ==="
        try
            set s to sheet 1 of window 1
            log "  role: " & (role of s)
            log "  subrole: " & (subrole of s)
        end try

        -- Sheet groups
        set grpCount to count of groups of sheet 1 of window 1
        log ""
        log "=== SHEET GROUPS (" & grpCount & " total) ==="

        repeat with g from 1 to grpCount
            log ""
            log "  [Group " & g & "]"
            set grp to group g of sheet 1 of window 1
            try
                log "    role: " & (role of grp)
            end try
            try
                log "    subrole: " & (subrole of grp)
            end try
            try
                log "    identifier: " & (identifier of grp)
            end try

            -- Buttons in group
            set grpBtns to every button of grp
            log "    buttons: " & (count of grpBtns)
            repeat with b from 1 to count of grpBtns
                set btn to item b of grpBtns
                log ""
                log "    [Group " & g & " Button " & b & "]"
                try
                    log "      name: " & (name of btn)
                end try
                try
                    log "      title: " & (title of btn)
                end try
                try
                    log "      description: " & (description of btn)
                end try
                try
                    log "      role: " & (role of btn)
                end try
                try
                    log "      subrole: " & (subrole of btn)
                end try
                try
                    log "      value: " & (value of btn)
                end try
                try
                    log "      help: " & (help of btn)
                end try
                try
                    log "      enabled: " & (enabled of btn)
                end try
                try
                    log "      focused: " & (focused of btn)
                end try
                try
                    log "      selected: " & (selected of btn)
                end try
                try
                    log "      position: " & (item 1 of (position of btn)) & "," & (item 2 of (position of btn))
                end try
                try
                    log "      size: " & (item 1 of (size of btn)) & "x" & (item 2 of (size of btn))
                end try
                try
                    log "      identifier: " & (identifier of btn)
                end try
                try
                    log "      accessibility description: " & (accessibility description of btn)
                end try
            end repeat

            -- Static texts in group
            set grpTexts to every static text of grp
            if (count of grpTexts) > 0 then
                log ""
                log "    static texts: " & (count of grpTexts)
                repeat with t from 1 to count of grpTexts
                    set txt to item t of grpTexts
                    try
                        log "      [Text " & t & "]: " & (value of txt)
                    end try
                end repeat
            end if

            -- Text fields in group
            set grpFields to every text field of grp
            if (count of grpFields) > 0 then
                log ""
                log "    text fields: " & (count of grpFields)
                repeat with f from 1 to count of grpFields
                    set fld to item f of grpFields
                    try
                        log "      [Field " & f & "]: value=" & (value of fld)
                    end try
                end repeat
            end if
        end repeat

        -- Toolbars
        log ""
        log "=== TOOLBARS ==="
        try
            set tbCount to count of toolbars of window 1
            log "  Window has " & tbCount & " toolbars"
            repeat with t from 1 to tbCount
                set tb to toolbar t of window 1
                set tbBtns to every button of tb
                log "  [Toolbar " & t & "] has " & (count of tbBtns) & " buttons"
                repeat with b from 1 to count of tbBtns
                    set btn to item b of tbBtns
                    log "    [Toolbar Button " & b & "]"
                    try
                        log "      title: " & (title of btn)
                    end try
                    try
                        log "      description: " & (description of btn)
                    end try
                    try
                        log "      subrole: " & (subrole of btn)
                    end try
                end repeat
            end repeat
        on error
            log "  No toolbars found"
        end try

        -- Full element dump of sheet
        log ""
        log "=== FULL SHEET CONTENTS ==="
        try
            set contents to entire contents of sheet 1 of window 1
            repeat with elem in contents
                try
                    set elemClass to class of elem as string
                    set elemRole to role of elem
                    set elemDesc to ""
                    try
                        set elemDesc to description of elem
                    end try
                    set elemName to ""
                    try
                        set elemName to name of elem
                    end try
                    set elemSubrole to ""
                    try
                        set elemSubrole to subrole of elem
                    end try
                    if elemClass contains "button" or elemSubrole contains "Button" or elemRole contains "Button" then
                        log "  " & elemClass & " | role:" & elemRole & " | subrole:" & elemSubrole & " | desc:" & elemDesc & " | name:" & elemName
                    end if
                end try
            end repeat
        end try

        log ""
        log "============================================"
        log "END DEBUG DUMP"
        log "============================================"
    end tell
end tell
EOF
