package gosshd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The whole reason this server exists is that it authenticates in-process.
// Win32-OpenSSH defers to Windows accounts, and WinPE's minimal lsass cannot
// mint the virtual-account logon its privsep child needs, so its sessions die
// before authentication. Nothing here may consult a Windows account.
func TestAuthenticate_AcceptsConfiguredCredentials(t *testing.T) {
	s := Server{User: "admin", Password: "admin"}

	assert.True(t, s.Authenticate("admin", "admin"))
}

func TestAuthenticate_RejectsWrongCredentials(t *testing.T) {
	s := Server{User: "admin", Password: "admin"}

	assert.False(t, s.Authenticate("admin", "wrong"), "wrong password")
	assert.False(t, s.Authenticate("root", "admin"), "wrong user")
	assert.False(t, s.Authenticate("", ""), "empty credentials")
}

// An unconfigured server must not fall open. A zero-value Server would
// otherwise accept the empty user with the empty password.
func TestAuthenticate_ZeroValueAcceptsNothing(t *testing.T) {
	var s Server

	assert.False(t, s.Authenticate("", ""))
	assert.False(t, s.Authenticate("admin", "admin"))
}

// The defaults are a contract with the host side: the harness connects with
// exactly these, and the guest is reached only over a per-run forwarded port.
func TestDefaults_AreTheCredentialsTheHostConnectsWith(t *testing.T) {
	s := Server{User: DefaultUser, Password: DefaultPassword}

	assert.True(t, s.Authenticate("admin", "admin"))
	assert.Equal(t, ":22", DefaultAddr)
}

// WinPE ships cmd.exe; pwsh is staged by the harness but is not guaranteed
// on PATH, so the shell has to be the one binary that is always there.
func TestSessionCommand_BareShellWhenNoCommandGiven(t *testing.T) {
	assert.Equal(t, []string{"cmd.exe"}, SessionCommand(""))
}

func TestSessionCommand_RunsRequestedCommandThroughCmd(t *testing.T) {
	assert.Equal(t, []string{"cmd.exe", "/c", "dir X:\\devcell"},
		SessionCommand(`dir X:\devcell`))
}
