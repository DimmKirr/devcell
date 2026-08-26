package qemu

// GoSSHDServeData is the template context the "gosshd-serve" partial needs.
// Embed it in a script's own data struct to include the partial:
//
//	data := struct {
//	    GoSSHDServeData
//	    Banner string
//	}{GoSSHDServeData: DefaultGoSSHDServeData(), Banner: "..."}
type GoSSHDServeData struct {
	// SSHExe is the server payload's filename on the agent volume.
	SSHExe string
	// ServerLog is the server's log filename on the agent volume.
	ServerLog string
}

// DefaultGoSSHDServeData wires the partial to the payload names the host
// stages.
func DefaultGoSSHDServeData() GoSSHDServeData {
	return GoSSHDServeData{
		SSHExe:    GoSSHDPayloadName,
		ServerLog: GoSSHDLogFile,
	}
}
