package qemu

// InteractiveShellCommand is the agent command for a hands-on
// troubleshooting session inside a booted image.
//
// It runs the keep-alive probe first — which brings up sshd — and only then
// hands the console to cmd.exe. Order matters: an SSH session is independent
// of the console, so it survives anything typed there, while the console
// itself is fragile. WinPE's shell is bootstrap.cmd running pwsh with
// `& goto :eof`, so interrupting the agent ends the batch and takes WinPE
// down with it. Parking the agent inside a blocking cmd.exe keeps that from
// happening and gives QMP keystrokes a prompt that actually executes them.
//
// -NoNewWindow with no redirection is the load-bearing detail: the child
// inherits the console's own stdin and stdout rather than the agent's
// pipeline, so typed keys run and their output is visible on screen.
func InteractiveShellCommand() string {
	return `& "$DevcellVol\` + KeepAliveScriptName + `" $DevcellVol; ` +
		`Start-Process -FilePath cmd.exe -NoNewWindow -Wait`
}
