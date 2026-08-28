package qemu

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// BuildSSHArgv constructs the SSH argv for running a command inside a Windows VM.
// For Windows guests, we use cmd.exe /c or powershell -NoProfile -Command.
func BuildSSHArgv(spec Spec) []string {
	userHost := spec.SSHUser + "@" + spec.SSHHost
	argv := []string{
		"ssh",
		"-p", fmt.Sprintf("%d", spec.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if spec.SSHKeyPath != "" {
		argv = append(argv, "-i", spec.SSHKeyPath)
	}
	argv = append(argv, userHost)

	// Build the remote command
	var tokens []string
	if len(spec.EnvVars) > 0 {
		// Windows: set env vars via PowerShell $env: syntax
		for _, kv := range spec.EnvVars {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				tokens = append(tokens, fmt.Sprintf("$env:%s='%s';", parts[0], parts[1]))
			}
		}
	}

	tokens = append(tokens, spec.Binary)
	tokens = append(tokens, spec.DefaultFlags...)
	tokens = append(tokens, spec.UserArgs...)

	if spec.ProjectDir != "" {
		basename := filepath.Base(spec.ProjectDir)
		prefix := fmt.Sprintf("cd ~\\%s;", basename)
		tokens = append([]string{prefix}, tokens...)
	}

	if len(tokens) > 0 {
		argv = append(argv, "powershell", "-NoProfile", "-Command",
			strings.Join(tokens, " "))
	}

	return argv
}

// PowerShellEncodedCommand wraps a script for SSH transport to a Windows
// guest. The command string a guest receives is re-parsed by its default
// shell and again by PowerShell's native argument handling, which eats
// unescaped double quotes — so any literal script with quoting eventually
// arrives mangled. -EncodedCommand sidesteps every parser in the chain:
// the script travels as base64 over UTF-16LE and PowerShell decodes it
// itself.
func PowerShellEncodedCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	raw := make([]byte, len(u16)*2)
	for i, v := range u16 {
		raw[i*2] = byte(v)
		raw[i*2+1] = byte(v >> 8)
	}
	return "powershell -NoProfile -EncodedCommand " + base64.StdEncoding.EncodeToString(raw)
}

// BuildSSHExecArgv constructs a simple SSH argv for running a single command.
// Used during provisioning.
func BuildSSHExecArgv(host string, port uint16, user, keyPath, command string) []string {
	argv := []string{
		"ssh",
		"-p", fmt.Sprintf("%d", port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
		// Unattended: fail fast on a rejected key rather than falling back to
		// password auth and trying to spawn ssh-askpass, which turns one clear
		// "Permission denied (publickey)" into a wall of askpass errors.
		"-o", "BatchMode=yes",
		// A long-running command must not be able to wedge the run: when the
		// guest side dies without closing the channel, ssh waits forever on a
		// connection the kernel still calls ESTABLISHED (a dev-env stage sat
		// like that for three hours). Probes every 30s, giving up after 20
		// unanswered — 10 minutes of genuine silence, far longer than any
		// legitimate gap in a chatty provisioning step.
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=20",
	}
	if keyPath != "" {
		argv = append(argv, "-i", keyPath)
	}
	argv = append(argv, user+"@"+host, command)
	return argv
}
