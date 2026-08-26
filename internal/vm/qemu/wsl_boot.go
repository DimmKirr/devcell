package qemu

import (
	"strings"
)

const (
	// WSLBootScriptName is the pass4 script's filename on the agent volume.
	WSLBootScriptName = `devcell-wsl-boot.ps1`

	WSLBootBanner   = `=== DEVCELL WSL BOOT ===`
	WSLBootComplete = `=== DEVCELL WSL BOOT COMPLETE ===`

	// WSLDistroName is the smoke-test distro registered by the pass4 script.
	WSLDistroName = `alpine`

	// WSLRootfsVolName is the rootfs tarball's filename on the agent volume.
	// The distro leg only runs when the test ships it.
	WSLRootfsVolName = `alpine-rootfs.tar.gz`
)

// WSLBootScriptCommand is the agent command line that runs the pass4 script.
func WSLBootScriptCommand() string {
	return `& "$DevcellVol\` + WSLBootScriptName + `" $DevcellVol`
}

// GenerateWSLBootScript produces the pass4 script: run the VMP runtime
// registration again (each boot is a fresh ramdisk), start vmcompute,
// register the transplanted WSL engine the way its MSI declares it —
// New-Service for WSLService, regsvr32 for the COM proxy stub — prove
// first contact with wsl --status, and when the volume carries a rootfs,
// import it and run uname inside.
func GenerateWSLBootScript() []byte {
	data := struct {
		StructPort string
		Banner     string
		Complete   string
		Distro     string
		Rootfs     string
	}{
		StructPort: StructuredPortName,
		Banner:     WSLBootBanner,
		Complete:   WSLBootComplete,
		Distro:     WSLDistroName,
		Rootfs:     WSLRootfsVolName,
	}

	out := renderTemplate("wsl-boot.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}
