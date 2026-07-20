package tart

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ExecSpec describes a command to run inside a tart VM via `tart exec`.
type ExecSpec struct {
	Binary     string   // binary to run (e.g. "zsh", "claude")
	Flags      []string // default flags for the binary
	UserArgs   []string // user-provided args
	EnvVars    []string // KEY=VAL pairs to set in the environment
	ProjectDir string   // host project path — basename is used for cd ~/basename
	RunAsUser  string   // if set, wrap command with sudo -u <user> -i
}

// BuildExecCommand constructs a shell command string for `tart exec <vm> bash -l -c <cmd>`.
// Sources the nix daemon profile, cds into the project dir, sets env vars,
// and runs the binary.
//
// When RunAsUser is set, the entire command is wrapped with
// `sudo -u <user> -i bash -l -c '...'` so the session runs as the
// specified user (matching Docker's HOST_USER model).
func BuildExecCommand(spec ExecSpec) string {
	var tokens []string
	if len(spec.EnvVars) > 0 {
		tokens = append(tokens, "env")
		tokens = append(tokens, spec.EnvVars...)
	}
	tokens = append(tokens, spec.Binary)
	tokens = append(tokens, spec.Flags...)
	tokens = append(tokens, spec.UserArgs...)

	agentCmd := shellJoinTokens(tokens)

	var cmd string
	if spec.ProjectDir != "" {
		basename := filepath.Base(spec.ProjectDir)
		cmd = "cd ~/" + shellQuoteToken(basename) + " && " + agentCmd
	} else {
		cmd = agentCmd
	}

	const nixSource = `. /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh 2>/dev/null || . "$HOME/.nix-profile/etc/profile.d/nix.sh" 2>/dev/null || true`
	innerCmd := nixSource + "; " + cmd

	if spec.RunAsUser != "" {
		return "sudo -u " + shellQuoteToken(spec.RunAsUser) + " -i bash -l -c " + shellQuoteToken(innerCmd)
	}
	return innerCmd
}

// BuildSSHArgv constructs the SSH argv for running a command inside a macOS VM:
//
//	ssh -p <port> -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
//	    <user>@<host> -t bash -l -c '<nix source; cd ~/project; env KEY=VAL binary flags>'
//
// The remote command is wrapped in `bash -l -c "..."` so that the login shell
// sources profiles and puts home-manager-installed binaries on PATH.
//
// When ProjectDir is set, the command cds into ~/basename(ProjectDir) first,
// mirroring Docker's --workdir behaviour.
//
// It is a pure function: no I/O, no exec.
func BuildSSHArgv(spec Spec, host string) []string {
	// Build the inner command tokens: [env KEY=VAL...] binary flags... args...
	var tokens []string
	if len(spec.EnvVars) > 0 {
		tokens = append(tokens, "env")
		tokens = append(tokens, spec.EnvVars...)
	}
	tokens = append(tokens, spec.Binary)
	tokens = append(tokens, spec.DefaultFlags...)
	tokens = append(tokens, spec.UserArgs...)

	// Shell-quote each token and join into a single string for bash -c.
	agentCmd := shellJoinTokens(tokens)

	// Prepend cd into the project workdir when ProjectDir is known.
	var remoteCmd string
	if spec.ProjectDir != "" {
		basename := filepath.Base(spec.ProjectDir)
		remoteCmd = "cd ~/" + shellQuoteToken(basename) + " && " + agentCmd
	} else {
		remoteCmd = agentCmd
	}

	// Explicitly source the nix daemon profile before running the agent binary.
	// Determinate installer puts the profile here; fall back to home-manager path.
	const nixSource = `. /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh 2>/dev/null || . "$HOME/.nix-profile/etc/profile.d/nix.sh" 2>/dev/null || true`
	remoteCmd = nixSource + "; " + remoteCmd

	// Build the SSH argv.
	userHost := spec.SSHUser + "@" + host
	argv := []string{
		"ssh",
		"-p", fmt.Sprintf("%d", spec.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if spec.SSHKeyPath != "" {
		argv = append(argv, "-i", spec.SSHKeyPath)
	}
	argv = append(argv, userHost, "-t", "bash", "-l", "-c", shellQuoteToken(remoteCmd))
	return argv
}

// shellJoinTokens shell-quotes each token and joins them with spaces,
// producing a string safe to pass as the argument to `bash -c`.
func shellJoinTokens(tokens []string) string {
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = shellQuoteToken(t)
	}
	return strings.Join(quoted, " ")
}

// shellQuoteToken wraps a token in single quotes, escaping any embedded
// single quotes as '\''. Values that are already safe (no special chars)
// are returned as-is for readability.
func shellQuoteToken(s string) string {
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' ||
			r == '/' || r == ':' || r == '=' || r == '@' || r == '+') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote with embedded ' escaped as '\''
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
