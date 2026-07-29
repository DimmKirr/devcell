package qemu

import (
	"strings"
	"testing"

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

func TestBuildSSHExecArgv_NoKey(t *testing.T) {
	argv := BuildSSHExecArgv("127.0.0.1", 2222, "devcell", "", "dir")
	joined := strings.Join(argv, " ")
	assert.NotContains(t, joined, "-i")
	assert.Contains(t, joined, "dir")
}
