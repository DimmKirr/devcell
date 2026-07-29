package qemu

import (
	"fmt"
	"path/filepath"
	"strings"
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

// BuildSSHExecArgv constructs a simple SSH argv for running a single command.
// Used during provisioning.
func BuildSSHExecArgv(host string, port uint16, user, keyPath, command string) []string {
	argv := []string{
		"ssh",
		"-p", fmt.Sprintf("%d", port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
	}
	if keyPath != "" {
		argv = append(argv, "-i", keyPath)
	}
	argv = append(argv, user+"@"+host, command)
	return argv
}
