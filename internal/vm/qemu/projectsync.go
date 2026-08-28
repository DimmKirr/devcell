package qemu

import (
	"fmt"
	"path/filepath"
)

// Project file sync (CELL-383, phase 1).
//
// Neither virtiofs (no virtiofsd on macOS hosts) nor 9p (no Windows client
// driver) can mount the project into the guest, so phase 1 copies it over
// the session's own SSH transport: push before exec so the agent sees the
// real tree, and — in "two-way" mode — pull it back on exit. A mount-based
// phase 2 (host SMB share / WinFsp+sshfs-win) is tracked in the ticket.

func sshOptionArgs(spec Spec) []string {
	args := []string{
		"-P", fmt.Sprintf("%d", spec.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if spec.SSHKeyPath != "" {
		args = append(args, "-i", spec.SSHKeyPath)
	}
	return args
}

// BuildProjectPushArgv builds the scp argv that copies the project tree into
// the guest's home (creating ~\<basename>, where BuildSSHArgv cd's to).
// Returns nil when the spec has no project directory.
func BuildProjectPushArgv(spec Spec) []string {
	if spec.ProjectDir == "" {
		return nil
	}
	argv := append([]string{"scp", "-r", "-q"}, sshOptionArgs(spec)...)
	return append(argv,
		spec.ProjectDir,
		spec.SSHUser+"@"+spec.SSHHost+":")
}

// BuildProjectPullArgv builds the scp argv that copies the guest's project
// tree back over the local one (write-back for "two-way" sync). The
// destination is the project's parent directory so the tree overlays in
// place. Returns nil when the spec has no project directory.
func BuildProjectPullArgv(spec Spec) []string {
	if spec.ProjectDir == "" {
		return nil
	}
	argv := append([]string{"scp", "-r", "-q"}, sshOptionArgs(spec)...)
	return append(argv,
		spec.SSHUser+"@"+spec.SSHHost+":"+filepath.Base(spec.ProjectDir),
		filepath.Dir(spec.ProjectDir))
}
