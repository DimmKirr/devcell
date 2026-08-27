package qemu

import (
	"strings"

	"github.com/devcell-sh/go-winkit/templates"
)

const (
	// HCSBootScriptName is the pass3 script's filename on the agent volume.
	HCSBootScriptName = `devcell-hcs-boot.ps1`

	// HCSBootExeName is the nested-VM smoke test binary, cross-compiled
	// from internal/hcsvm/hcsboot and shipped on the shared volume.
	HCSBootExeName = `hcsboot.exe`

	// HCSThumbnailName is the raw RGB565 frame the script saves if the
	// vmms thumbnail API is available. 640x480, 2 bytes per pixel.
	HCSThumbnailName = `hcs-thumbnail.rgb565`

	HCSBootBanner   = `=== DEVCELL HCS BOOT ===`
	HCSBootComplete = `=== DEVCELL HCS BOOT COMPLETE ===`
)

// HCSBootScriptCommand is the agent command line that runs the pass3 script.
func HCSBootScriptCommand() string {
	return `& "$DevcellVol\` + HCSBootScriptName + `" $DevcellVol`
}

// GenerateHCSBootScript produces the pass3 script: register the runtime
// pieces, start vmcompute, boot a diskless Gen2 VM through HCS via
// hcsboot.exe, and — when the vmms trio is present — register its WMI
// provider and grab a thumbnail of the nested VM's screen.
func GenerateHCSBootScript() []byte {
	data := struct {
		StructPort string
		Banner     string
		Complete   string
	}{
		StructPort: StructuredPortName,
		Banner:     HCSBootBanner,
		Complete:   HCSBootComplete,
	}

	out := templates.Render("hcs-boot.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}
