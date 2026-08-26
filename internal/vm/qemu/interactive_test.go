package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The interactive session is for troubleshooting a booted image by hand.
// It brings sshd up first — an SSH session survives anything done to the
// console, whereas interrupting the console kills winpeshl and the VM —
// then hands the console to cmd.exe so QMP keystrokes reach a real prompt.
func TestInteractiveShellCommand_StartsSSHDThenHandsOverConsole(t *testing.T) {
	cmd := InteractiveShellCommand()

	assert.Contains(t, cmd, KeepAliveScriptName,
		"the probe must run first so sshd is listening before the console is taken")
	assert.Contains(t, cmd, "cmd.exe")

	// -NoNewWindow without redirection gives the child the console's own
	// stdin/stdout, which is what makes typed keys execute; -Wait keeps the
	// agent parked so WinPE does not shut down.
	assert.Contains(t, cmd, "-NoNewWindow")
	assert.Contains(t, cmd, "-Wait")

	assert.Less(t, strings.Index(cmd, KeepAliveScriptName), strings.Index(cmd, "cmd.exe"),
		"sshd must be up before the console blocks on cmd.exe")
}
