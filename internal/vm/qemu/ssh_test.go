package qemu

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
)

func TestBuildSSHArgv_Basic(t *testing.T) {
	s := Spec{
		SSHUser:  "devcell",
		SSHHost:  "127.0.0.1",
		SSHPort:  2222,
		Binary:   "cmd.exe",
		EnvVars:  nil,
		UserArgs: []string{"/c", "echo hello"},
	}
	argv := BuildSSHArgv(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "ssh")
	assert.Contains(t, joined, "-p 2222")
	assert.Contains(t, joined, "devcell@127.0.0.1")
	assert.Contains(t, joined, "cmd.exe")
}

func TestBuildSSHArgv_WithKey(t *testing.T) {
	s := Spec{
		SSHUser:    "devcell",
		SSHHost:    "127.0.0.1",
		SSHPort:    2222,
		SSHKeyPath: "/home/user/.ssh/id_ed25519",
		Binary:     "powershell",
	}
	argv := BuildSSHArgv(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "-i /home/user/.ssh/id_ed25519")
}

func TestBuildSSHArgv_WithEnvVars(t *testing.T) {
	s := Spec{
		SSHUser: "devcell",
		SSHHost: "127.0.0.1",
		SSHPort: 2222,
		Binary:  "cmd.exe",
		EnvVars: []string{"FOO=bar", "BAZ=qux"},
	}
	argv := BuildSSHArgv(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "$env:FOO='bar'")
	assert.Contains(t, joined, "$env:BAZ='qux'")
}

func TestBuildSSHArgv_WithProjectDir(t *testing.T) {
	s := Spec{
		SSHUser:    "devcell",
		SSHHost:    "127.0.0.1",
		SSHPort:    2222,
		Binary:     "code",
		ProjectDir: "/Users/dev/myproject",
	}
	argv := BuildSSHArgv(s)
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, `cd ~\myproject`)
}

func TestBuildSSHExecArgv(t *testing.T) {
	argv := BuildSSHExecArgv("127.0.0.1", 2222, "devcell", "/tmp/key", "whoami")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "ssh")
	assert.Contains(t, joined, "-p 2222")
	assert.Contains(t, joined, "devcell@127.0.0.1")
	assert.Contains(t, joined, "-i /tmp/key")
	assert.Contains(t, joined, "whoami")
}

// This argv only ever runs unattended (build provisioning), where a password
// prompt has nobody to answer it. Without BatchMode, a key the guest does not
// accept makes ssh fall back to password and keyboard-interactive auth and try
// to spawn ssh-askpass — which on a nix host is not even installed:
//
//	ssh_askpass: exec(.../libexec/ssh-askpass): No such file or directory
//	Permission denied, please try again.
//
// Three retries of that buried the actual cause (wrong username) under askpass
// noise in run 20260730T222409.
func TestBuildSSHExecArgv_RefusesInteractiveAuthPrompts(t *testing.T) {
	joined := strings.Join(BuildSSHExecArgv("127.0.0.1", 2222, "dmitry", "/tmp/key", "whoami"), " ")

	assert.Contains(t, joined, "BatchMode=yes",
		"unattended ssh must fail fast instead of prompting for a password")
}

// Run 20260731T205544 (run 7): provisioning scripts were sent as
// `powershell -NoProfile -Command '<script>'` through an sshd whose default
// shell is itself PowerShell. The guest re-parses that string through the
// Windows command line, which strips the inner double quotes —
// `$sshDir = "$env:USERPROFILE\.ssh"` arrived as `$sshDir = $env:USERPROFILE\.ssh`,
// a ParserError that failed all three attempts and aborted an otherwise clean
// 1h12m install. -EncodedCommand carries the script as base64(UTF-16LE), so no
// quote survives into any shell's parser on either side.
func TestPowerShellEncodedCommand_RoundTripsQuotes(t *testing.T) {
	script := "$sshDir = \"$env:USERPROFILE\\.ssh\"\r\nWrite-Output 'it''s fine'"
	cmd := PowerShellEncodedCommand(script)

	const prefix = "powershell -NoProfile -EncodedCommand "
	if !strings.HasPrefix(cmd, prefix) {
		t.Fatalf("PowerShellEncodedCommand() = %q, want %q prefix", cmd, prefix)
	}
	payload := strings.TrimPrefix(cmd, prefix)

	// The transport must contain nothing any shell can reinterpret.
	assert.NotContains(t, payload, `"`)
	assert.NotContains(t, payload, "'")
	assert.NotContains(t, payload, " ")

	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("payload is %d bytes, not valid UTF-16LE", len(raw))
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	assert.Equal(t, script, string(utf16.Decode(u16)),
		"script must round-trip byte-identical through the transport")
}

func TestBuildSSHExecArgv_NoKey(t *testing.T) {
	argv := BuildSSHExecArgv("127.0.0.1", 2222, "devcell", "", "dir")
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "-i")
	assert.Contains(t, joined, "dir")
}

// Dev-env iteration 12 wedged for 3 hours: the guest-side PowerShell was gone
// and the WSL distro Stopped, yet the host's ssh sat in ESTABLISHED with an
// empty queue, waiting for a channel close that never came. Without
// keepalives ssh cannot tell a dead peer from a slow one — and `cell build`
// provisioning uses this same argv, so a wedged connection would hang a build
// forever.
func TestBuildSSHExecArgv_DetectsADeadPeer(t *testing.T) {
	joined := strings.Join(BuildSSHExecArgv("127.0.0.1", 2222, "dmitry", "/tmp/key", "whoami"), " ")

	assert.Contains(t, joined, "ServerAliveInterval",
		"unattended ssh must probe the peer or a wedged session hangs forever")
	assert.Contains(t, joined, "ServerAliveCountMax",
		"probes need a bound — otherwise they never conclude the peer is dead")
}
