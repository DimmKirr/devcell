package qemu

import (
	"strings"
	"testing"
)

// --- Project file sync (CELL-383, phase 1: scp over the SSH channel) ---
//
// The guest previously landed in an empty ~\<project> directory. Phase 1
// pushes the project tree over the session's own SSH transport (no host SMB
// setup, no guest drivers); "two-way" additionally pulls it back on exit.

func syncSpec() Spec {
	return Spec{
		VMName:     "cell1",
		SSHHost:    "host.docker.internal",
		SSHPort:    2222,
		SSHUser:    "devcell",
		SSHKeyPath: "/home/u/.devcell/cell1/qemu/id_ed25519",
		ProjectDir: "/devcell-155",
	}
}

func TestBuildProjectPushArgv(t *testing.T) {
	argv := BuildProjectPushArgv(syncSpec())
	s := strings.Join(argv, " ")
	if argv[0] != "scp" {
		t.Fatalf("argv[0] = %q, want scp", argv[0])
	}
	for _, want := range []string{
		"-r",
		"-P 2222",
		"-i /home/u/.devcell/cell1/qemu/id_ed25519",
		"StrictHostKeyChecking=no",
		"/devcell-155",
		"devcell@host.docker.internal:",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("push argv must contain %q, got: %s", want, s)
		}
	}
}

func TestBuildProjectPushArgv_SourceBeforeDest(t *testing.T) {
	argv := BuildProjectPushArgv(syncSpec())
	src, dst := -1, -1
	for i, a := range argv {
		if a == "/devcell-155" {
			src = i
		}
		if strings.HasPrefix(a, "devcell@") {
			dst = i
		}
	}
	if src < 0 || dst < 0 || src > dst {
		t.Errorf("push must copy local → remote (src idx %d, dst idx %d): %v", src, dst, argv)
	}
}

func TestBuildProjectPullArgv(t *testing.T) {
	argv := BuildProjectPullArgv(syncSpec())
	s := strings.Join(argv, " ")
	for _, want := range []string{
		"-r",
		"-P 2222",
		"devcell@host.docker.internal:devcell-155",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pull argv must contain %q, got: %s", want, s)
		}
	}
	// Destination is the project's parent so the tree overlays in place.
	if argv[len(argv)-1] != "/" {
		t.Errorf("pull dest must be the project parent dir, got %q", argv[len(argv)-1])
	}
}

func TestBuildProjectSyncArgv_NoKeyOmitsIdentity(t *testing.T) {
	s := syncSpec()
	s.SSHKeyPath = ""
	if joined := strings.Join(BuildProjectPushArgv(s), " "); strings.Contains(joined, "-i ") {
		t.Errorf("no key path must omit -i, got: %s", joined)
	}
}

func TestBuildProjectSyncArgv_EmptyProjectDirReturnsNil(t *testing.T) {
	s := syncSpec()
	s.ProjectDir = ""
	if argv := BuildProjectPushArgv(s); argv != nil {
		t.Errorf("no project dir → no push argv, got %v", argv)
	}
	if argv := BuildProjectPullArgv(s); argv != nil {
		t.Errorf("no project dir → no pull argv, got %v", argv)
	}
}
