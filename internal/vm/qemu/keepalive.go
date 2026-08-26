package qemu

import "strings"

const (
	// KeepAliveScriptName is the probe script's filename on the agent volume.
	KeepAliveScriptName = `devcell-keepalive.ps1`

	// KeepAliveProbeFile is a host-written file on the shared FAT volume.
	// Echoing it back proves the host->guest file channel end to end.
	KeepAliveProbeFile = `devcell-probe.txt`

	KeepAliveBanner = `=== DEVCELL KEEPALIVE PROBE ===`
)

// KeepAliveProbeCommand is the agent command line for the probe script.
func KeepAliveProbeCommand() string {
	return `& "$DevcellVol\` + KeepAliveScriptName + `" $DevcellVol`
}

// GenerateKeepAliveScript produces the probe a troubleshooting session opens
// with: echo back the file the host placed on the FAT volume, confirm the
// agent shell ran, and dump enough state to debug from. The VM is left
// running afterwards, so this output is the last thing written before the
// guest goes idle and waits to be driven through QMP.
func GenerateKeepAliveScript() []byte {
	data := struct {
		GoSSHDServeData
		StructPort string
		Banner     string
		ProbeFile  string
	}{
		GoSSHDServeData: DefaultGoSSHDServeData(),
		StructPort:      StructuredPortName,
		Banner:          KeepAliveBanner,
		ProbeFile:       KeepAliveProbeFile,
	}

	out := renderTemplate("keepalive.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}
