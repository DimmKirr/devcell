package tart_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/tart"
)

// tartSpec builds a minimal Spec for testing.
func tartSpec(extra ...func(*tart.Spec)) tart.Spec {
	spec := tart.Spec{
		DiskPath:     "/path/to/disk.img",
		CPUs:         4,
		MemoryGB:     4,
		SSHPort:      2222,
		SSHUser:      "devcell",
		Binary:       "claude",
		DefaultFlags: []string{"--dangerously-skip-permissions"},
	}
	for _, fn := range extra {
		fn(&spec)
	}
	return spec
}

const testHost = "localhost"

func TestBuildSSHArgv_ContainsSSH(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	if len(argv) == 0 || argv[0] != "ssh" {
		t.Fatalf("expected argv[0]='ssh', got %v", argv)
	}
}

func TestBuildSSHArgv_PortFlag(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	found := false
	for i, a := range argv {
		if a == "-p" && i+1 < len(argv) && argv[i+1] == "2222" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -p 2222 in argv: %v", argv)
	}
}

func TestBuildSSHArgv_StrictHostKeyOff(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "StrictHostKeyChecking=no") {
		t.Errorf("expected StrictHostKeyChecking=no in argv: %v", argv)
	}
}

func TestBuildSSHArgv_UserHost(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	found := false
	for _, a := range argv {
		if a == "devcell@localhost" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'devcell@localhost' in argv: %v", argv)
	}
}

func TestBuildSSHArgv_HasLoginBashWrapper(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "bash -l -c") {
		t.Errorf("expected 'bash -l -c' (login shell) in argv: %v", argv)
	}
}

func TestBuildSSHArgv_RunsCorrectBinary(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "claude") {
		t.Errorf("expected binary 'claude' in argv: %v", argv)
	}
}

func TestBuildSSHArgv_DefaultFlagsIncluded(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Errorf("expected default flags in argv: %v", argv)
	}
}

func TestBuildSSHArgv_UserArgsAppended(t *testing.T) {
	spec := tartSpec(func(s *tart.Spec) {
		s.UserArgs = []string{"--model", "opus"}
	})
	argv := tart.BuildSSHArgv(spec, testHost)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--model") || !strings.Contains(joined, "opus") {
		t.Errorf("expected user args in argv: %v", argv)
	}
}

func TestBuildSSHArgv_EnvVarsInjectedBeforeBinary(t *testing.T) {
	spec := tartSpec(func(s *tart.Spec) {
		s.EnvVars = []string{"TERM=xterm-256color", "LANG=en_US.UTF-8"}
	})
	argv := tart.BuildSSHArgv(spec, testHost)

	// The remote command is the last argv element (the bash -c argument).
	remoteCmd := argv[len(argv)-1]

	envIdx := strings.Index(remoteCmd, "env ")
	binaryIdx := strings.Index(remoteCmd, "claude")
	if envIdx == -1 {
		t.Fatalf("'env' not found in remote command when EnvVars set: %q", remoteCmd)
	}
	if binaryIdx == -1 {
		t.Fatalf("'claude' not found in remote command: %q", remoteCmd)
	}
	if envIdx >= binaryIdx {
		t.Errorf("'env' must appear before binary in remote command: %q", remoteCmd)
	}
}

func TestBuildSSHArgv_ProjectDirCdPrefix(t *testing.T) {
	spec := tartSpec(func(s *tart.Spec) {
		s.ProjectDir = "/Users/dmitry/dev/myproject"
	})
	argv := tart.BuildSSHArgv(spec, testHost)
	remoteCmd := argv[len(argv)-1]

	nixIdx := strings.Index(remoteCmd, "nix-profile")
	cdIdx := strings.Index(remoteCmd, "cd ~/myproject")
	if cdIdx == -1 {
		t.Errorf("expected remote command to contain 'cd ~/myproject', got: %q", remoteCmd)
	}
	if nixIdx != -1 && cdIdx < nixIdx {
		t.Errorf("expected 'cd ~/myproject' to come after nix source prefix, got: %q", remoteCmd)
	}
}

func TestBuildSSHArgv_NoProjectDirNoCd(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	remoteCmd := argv[len(argv)-1]
	if strings.Contains(remoteCmd, "cd ~/") {
		t.Errorf("expected no cd prefix when ProjectDir is empty: %q", remoteCmd)
	}
}

func TestBuildSSHArgv_NixSourcePrefix(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	remoteCmd := argv[len(argv)-1]
	if !strings.Contains(remoteCmd, `nix-profile/etc/profile.d/nix.sh`) {
		t.Errorf("expected nix.sh sourced in remote command: %q", remoteCmd)
	}
	// nix source must come before the binary
	nixIdx := strings.Index(remoteCmd, "nix-profile")
	binaryIdx := strings.Index(remoteCmd, "claude")
	if nixIdx == -1 || binaryIdx == -1 || nixIdx >= binaryIdx {
		t.Errorf("nix source must precede binary in remote command: %q", remoteCmd)
	}
}

func TestBuildSSHArgv_SSHKeyPath(t *testing.T) {
	spec := tartSpec(func(s *tart.Spec) {
		s.SSHKeyPath = "/home/user/.ssh/id_ed25519"
	})
	argv := tart.BuildSSHArgv(spec, testHost)
	found := false
	for i, a := range argv {
		if a == "-i" && i+1 < len(argv) && argv[i+1] == "/home/user/.ssh/id_ed25519" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -i /home/user/.ssh/id_ed25519 in argv: %v", argv)
	}
}

func TestBuildSSHArgv_NoSSHKeyPath(t *testing.T) {
	argv := tart.BuildSSHArgv(tartSpec(), testHost)
	for _, a := range argv {
		if a == "-i" {
			t.Errorf("unexpected -i flag when SSHKeyPath is empty: %v", argv)
		}
	}
}

func TestBuildSSHArgv_ShellQuoting(t *testing.T) {
	spec := tartSpec(func(s *tart.Spec) {
		s.UserArgs = []string{"--prompt", "hello world"}
	})
	argv := tart.BuildSSHArgv(spec, testHost)
	remoteCmd := argv[len(argv)-1]
	// "hello world" contains a space and must be quoted in the remote command.
	if !strings.Contains(remoteCmd, "'hello world'") {
		t.Errorf("expected quoted 'hello world' in remote command: %q", remoteCmd)
	}
}

// --- BuildExecCommand tests ---

func TestBuildExecCommand_ContainsBinary(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{Binary: "claude"})
	if !strings.Contains(cmd, "claude") {
		t.Errorf("expected 'claude' in exec command, got: %q", cmd)
	}
}

func TestBuildExecCommand_NixSource(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{Binary: "zsh"})
	if !strings.Contains(cmd, "nix-daemon.sh") {
		t.Errorf("expected nix-daemon.sh source in exec command, got: %q", cmd)
	}
}

func TestBuildExecCommand_ProjectDirCd(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{
		Binary:     "claude",
		ProjectDir: "/Users/dmitry/dev/myproject",
	})
	if !strings.Contains(cmd, "cd ~/myproject") {
		t.Errorf("expected 'cd ~/myproject' in exec command, got: %q", cmd)
	}
}

func TestBuildExecCommand_EnvVars(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{
		Binary:  "claude",
		EnvVars: []string{"TERM=xterm-256color", "TZ=UTC"},
	})
	if !strings.Contains(cmd, "env") {
		t.Errorf("expected 'env' prefix for env vars, got: %q", cmd)
	}
	if !strings.Contains(cmd, "TERM=xterm-256color") {
		t.Errorf("expected TERM in exec command, got: %q", cmd)
	}
}

func TestBuildExecCommand_RunAsUser(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{
		Binary:    "claude",
		RunAsUser: "dmitry",
	})
	if !strings.Contains(cmd, "sudo -u dmitry -i") {
		t.Errorf("expected 'sudo -u dmitry -i' in exec command, got: %q", cmd)
	}
	if !strings.Contains(cmd, "bash -l -c") {
		t.Errorf("expected 'bash -l -c' wrapper for su, got: %q", cmd)
	}
}

func TestBuildExecCommand_NoRunAsUser(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{Binary: "zsh"})
	if strings.Contains(cmd, "sudo -u") {
		t.Errorf("expected no sudo wrapper when RunAsUser is empty, got: %q", cmd)
	}
}

func TestBuildExecCommand_RunAsUserWithProjectDir(t *testing.T) {
	cmd := tart.BuildExecCommand(tart.ExecSpec{
		Binary:     "claude",
		ProjectDir: "/Users/dmitry/dev/devcell",
		RunAsUser:  "dmitry",
	})
	if !strings.Contains(cmd, "sudo -u dmitry -i") {
		t.Errorf("expected sudo wrapper, got: %q", cmd)
	}
	if !strings.Contains(cmd, "cd ~/devcell") {
		t.Errorf("expected 'cd ~/devcell' inside wrapped command, got: %q", cmd)
	}
}

func TestCreateSessionUserScript(t *testing.T) {
	script := tart.GenerateCreateSessionUserScript("dmitry")
	for _, want := range []string{
		`USERNAME="dmitry"`,
		"dscl . -create",
		"UserShell /bin/zsh",
		"NFSHomeDirectory",
		"dseditgroup",
		"sudoers.d",
		"NOPASSWD",
		"set -e",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("expected %q in create user script, got:\n%s", want, script)
		}
	}
}

func TestCreateSessionUserScript_Idempotent(t *testing.T) {
	script := tart.GenerateCreateSessionUserScript("dmitry")
	if !strings.Contains(script, "already exists") {
		t.Errorf("expected idempotent check in create user script, got:\n%s", script)
	}
}

func TestSetupSessionHomeScript(t *testing.T) {
	script := tart.GenerateSetupSessionHomeScript("dmitry")
	for _, want := range []string{
		"My Shared Files/home",
		"/Users/$USERNAME",
		"set -e",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("expected %q in setup home script, got:\n%s", want, script)
		}
	}
}
