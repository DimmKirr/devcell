package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
)

// vagrantSpec builds a minimal VagrantSpec for testing.
func vagrantSpec(extra ...func(*runner.VagrantSpec)) runner.VagrantSpec {
	spec := runner.VagrantSpec{
		Config:       config.Load("/home/bob/myproject", func(k string) string { return "" }),
		CellCfg:      cfg.CellConfig{},
		Binary:       "claude",
		DefaultFlags: []string{"--dangerously-skip-permissions"},
		UserArgs:     nil,
		VagrantDir:   "/home/bob/myproject/.devcell/vagrant",
		Provider:     "utm",
	}
	for _, fn := range extra {
		fn(&spec)
	}
	return spec
}

// L1: Pure unit tests — BuildVagrantSSHArgv is a pure function; no I/O.

func TestBuildVagrantSSHArgv_ContainsVagrantSSH(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	if len(argv) < 2 || argv[0] != "vagrant" || argv[1] != "ssh" {
		t.Fatalf("expected argv[0..1]=[vagrant ssh], got %v", argv)
	}
	// VagrantDir is NOT in the argv — caller sets cmd.Dir instead.
	for _, a := range argv {
		if a == "--chdir" {
			t.Errorf("--chdir must not appear in argv (use cmd.Dir): %v", argv)
		}
	}
}

func TestBuildVagrantSSHArgv_HasLoginBashWrapper(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	// Remote command must be wrapped in bash -l -c "..." so nix profile is sourced.
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "bash -l -c") {
		t.Errorf("expected 'bash -l -c' (login shell) in argv: %v", argv)
	}
}

func TestBuildVagrantSSHArgv_RunsCorrectBinary(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "claude") {
		t.Errorf("expected binary 'claude' in argv: %v", argv)
	}
}

func TestBuildVagrantSSHArgv_DefaultFlagsIncluded(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Errorf("expected default flags in argv: %v", argv)
	}
}

func TestBuildVagrantSSHArgv_UserArgsAppended(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.UserArgs = []string{"--model", "opus"}
	})
	argv := runner.BuildVagrantSSHArgv(spec)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--model") || !strings.Contains(joined, "opus") {
		t.Errorf("expected user args in argv: %v", argv)
	}
}

func TestBuildVagrantSSHArgv_VagrantDirNotInArgv(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.VagrantDir = "/custom/vagrant/dir"
	})
	argv := runner.BuildVagrantSSHArgv(spec)
	joined := strings.Join(argv, " ")
	// VagrantDir must NOT appear in argv; the caller sets cmd.Dir instead.
	if strings.Contains(joined, "/custom/vagrant/dir") {
		t.Errorf("VagrantDir must not appear in argv (use cmd.Dir): %v", argv)
	}
}

func TestBuildVagrantSSHArgv_NoDuplicateFlags(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	seen := map[string]int{}
	for _, a := range argv {
		seen[a]++
	}
	for k, c := range seen {
		if c > 1 && strings.HasPrefix(k, "--") {
			t.Errorf("flag %q appears %d times in argv %v", k, c, argv)
		}
	}
}

// L1: VagrantStatus parsing — pure function, no I/O.
// All tests use --machine-readable CSV format (timestamp,target,type,data).

func TestVagrantStatusRunning_UTMStarted(t *testing.T) {
	out := "1776348449,default,provider-name,utm\n1776348449,default,state,started\n"
	if !runner.VagrantStatusRunning(out) {
		t.Errorf("expected running=true for UTM started: %q", out)
	}
}

func TestVagrantStatusRunning_LibvirtRunning(t *testing.T) {
	out := "1776348449,default,provider-name,libvirt\n1776348449,default,state,running\n"
	if !runner.VagrantStatusRunning(out) {
		t.Errorf("expected running=true for libvirt running: %q", out)
	}
}

func TestVagrantStatusRunning_PowerOff(t *testing.T) {
	out := "1776348449,default,provider-name,utm\n1776348449,default,state,poweroff\n"
	if runner.VagrantStatusRunning(out) {
		t.Errorf("expected running=false for poweroff: %q", out)
	}
}

func TestVagrantStatusRunning_Aborted(t *testing.T) {
	out := "1776348449,default,provider-name,utm\n1776348449,default,state,aborted\n"
	if runner.VagrantStatusRunning(out) {
		t.Errorf("expected running=false for aborted: %q", out)
	}
}

func TestVagrantStatusRunning_EmptyOutput(t *testing.T) {
	if runner.VagrantStatusRunning("") {
		t.Error("expected running=false for empty output")
	}
}

// L1: EnvVars injection — BuildVagrantSSHArgv is pure; no I/O.

func TestBuildVagrantSSHArgv_EnvVarsInjectedBeforeBinary(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.EnvVars = []string{"TERM=xterm-256color", "LANG=en_US.UTF-8"}
	})
	argv := runner.BuildVagrantSSHArgv(spec)

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

func TestBuildVagrantSSHArgv_EnvVarValuesPresent(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.EnvVars = []string{"TERM=xterm-256color", "LANG=en_US.UTF-8"}
	})
	argv := runner.BuildVagrantSSHArgv(spec)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "TERM=xterm-256color") {
		t.Errorf("expected TERM env var in argv: %v", argv)
	}
	if !strings.Contains(joined, "LANG=en_US.UTF-8") {
		t.Errorf("expected LANG env var in argv: %v", argv)
	}
}

func TestBuildVagrantSSHArgv_NoEnvCommandWhenNoEnvVars(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	remoteCmd := argv[len(argv)-1]
	if strings.HasPrefix(remoteCmd, "env ") {
		t.Errorf("remote command should not start with 'env' when no EnvVars: %q", remoteCmd)
	}
}

// L1: ProjectDir workdir — BuildVagrantSSHArgv is pure; no I/O.

func TestBuildVagrantSSHArgv_ProjectDirCdPrefix(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.ProjectDir = "/Users/dmitry/dev/myproject"
	})
	argv := runner.BuildVagrantSSHArgv(spec)
	remoteCmd := argv[len(argv)-1]
	// The nix profile source prefix comes first, then the cd; check both are present in order.
	nixIdx := strings.Index(remoteCmd, "nix-profile")
	cdIdx := strings.Index(remoteCmd, "cd ~/myproject")
	if cdIdx == -1 {
		t.Errorf("expected remote command to contain 'cd ~/myproject', got: %q", remoteCmd)
	}
	if nixIdx != -1 && cdIdx < nixIdx {
		t.Errorf("expected 'cd ~/myproject' to come after nix source prefix, got: %q", remoteCmd)
	}
}

func TestBuildVagrantSSHArgv_ProjectDirCdThenBinary(t *testing.T) {
	spec := vagrantSpec(func(s *runner.VagrantSpec) {
		s.ProjectDir = "/Users/dmitry/dev/myproject"
	})
	argv := runner.BuildVagrantSSHArgv(spec)
	remoteCmd := argv[len(argv)-1]
	cdIdx := strings.Index(remoteCmd, "cd ~/myproject")
	binaryIdx := strings.Index(remoteCmd, "claude")
	if cdIdx == -1 {
		t.Fatalf("cd not found in remote command: %q", remoteCmd)
	}
	if binaryIdx == -1 {
		t.Fatalf("binary not found in remote command: %q", remoteCmd)
	}
	if cdIdx >= binaryIdx {
		t.Errorf("cd must appear before binary: %q", remoteCmd)
	}
}

func TestBuildVagrantSSHArgv_NoProjectDirNoCd(t *testing.T) {
	argv := runner.BuildVagrantSSHArgv(vagrantSpec())
	remoteCmd := argv[len(argv)-1]
	if strings.Contains(remoteCmd, "cd ~/") {
		t.Errorf("expected no cd prefix when ProjectDir is empty: %q", remoteCmd)
	}
}

// L2: VagrantBinaryExists — returns false when vagrant command fails (no VM).

func TestVagrantBinaryExists_ReturnsFalseWhenVagrantFails(t *testing.T) {
	// An empty temp dir has no Vagrantfile; vagrant ssh will fail → binary absent.
	exists := runner.VagrantBinaryExists(context.Background(), t.TempDir(), "claude")
	if exists {
		t.Error("expected false when vagrant command fails (no VM)")
	}
}

// L1: VagrantIsRunning fast-path — returns false immediately when no Vagrantfile present.
// No vagrant subprocess is spawned, so this test is safe to run anywhere.

func TestVagrantIsRunning_NoVagrantfile(t *testing.T) {
	// Empty temp dir has no Vagrantfile — must return false without calling vagrant.
	if runner.VagrantIsRunning(t.TempDir()) {
		t.Error("expected false when no Vagrantfile present")
	}
}

func TestVagrantIsRunning_EmptyDir(t *testing.T) {
	if runner.VagrantIsRunning("") {
		t.Error("expected false for empty dir")
	}
}

func TestVagrantIsRunning_NonExistentDir(t *testing.T) {
	if runner.VagrantIsRunning("/nonexistent/path/that/cannot/exist") {
		t.Error("expected false for non-existent dir")
	}
}

// L1: VagrantMachineCreated — pure file check, no subprocess.

func TestVagrantMachineCreated_WithIDFile(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, ".vagrant", "machines", "default", "utm", "id")
	if err := os.MkdirAll(filepath.Dir(idPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idPath, []byte("E0F4A259-5FCA-4B08-BD30-6A707B1D35B6"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !runner.VagrantMachineCreated(dir) {
		t.Error("expected true when id file exists with content")
	}
}

func TestVagrantMachineCreated_EmptyIDFile(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, ".vagrant", "machines", "default", "utm", "id")
	if err := os.MkdirAll(filepath.Dir(idPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if runner.VagrantMachineCreated(dir) {
		t.Error("expected false when id file is empty (VM destroyed)")
	}
}

func TestVagrantMachineCreated_NoMachinesDir(t *testing.T) {
	if runner.VagrantMachineCreated(t.TempDir()) {
		t.Error("expected false when no .vagrant/machines dir")
	}
}

// L1: ParseVagrantGlobalStatus — pure function, no I/O.

func TestParseVagrantGlobalStatus_RunningVM(t *testing.T) {
	// UTM uses "started" state, not "running".
	output := `id       name    provider  state    directory
-------------------------------------------------------
abc1234  default utm       started  /Users/dmitry/dev/devcell/.devcell
`
	result := runner.ParseVagrantGlobalStatus(output)
	if len(result) != 1 {
		t.Fatalf("want 1 entry, got %d: %v", len(result), result)
	}
	machineID, ok := result["devcell"]
	if !ok {
		t.Fatalf("want key 'devcell', got %v", result)
	}
	if machineID != "abc1234" {
		t.Errorf("want machineID 'abc1234', got %q", machineID)
	}
}

func TestParseVagrantGlobalStatus_AcceptsRunningState(t *testing.T) {
	// Non-UTM providers (parallels, qemu) report "running".
	output := `id       name    provider  state    directory
-------------------------------------------------------
abc1234  default parallels running  /Users/dmitry/dev/proj1/.devcell
`
	result := runner.ParseVagrantGlobalStatus(output)
	if len(result) != 1 {
		t.Fatalf("want 1 entry for 'running' state, got %d: %v", len(result), result)
	}
}

func TestParseVagrantGlobalStatus_SkipsStopped(t *testing.T) {
	output := `id       name    provider  state    directory
-------------------------------------------------------
abc1234  default utm       started  /Users/dmitry/dev/proj1/.devcell
def5678  default utm       stopped  /Users/dmitry/dev/proj2/.devcell
`
	result := runner.ParseVagrantGlobalStatus(output)
	if len(result) != 1 {
		t.Fatalf("want only started VMs, got %d: %v", len(result), result)
	}
	if id, ok := result["proj1"]; !ok {
		t.Errorf("expected 'proj1' in result, got %v", result)
	} else if id != "abc1234" {
		t.Errorf("want machineID 'abc1234', got %q", id)
	}
}

func TestParseVagrantGlobalStatus_SkipsNonDevcellDirs(t *testing.T) {
	// VMs not in a .devcell directory should be ignored (not devcell cells).
	output := `id       name    provider  state    directory
-------------------------------------------------------
abc1234  default parallels running  /Users/dmitry/dev/claudelibs
def5678  default qemu      running  /Users/dmitry/dev/devcell/test/vagrant
ghi9012  default utm       started  /Users/dmitry/dev/myproject/.devcell
`
	result := runner.ParseVagrantGlobalStatus(output)
	if len(result) != 1 {
		t.Fatalf("want only .devcell VMs, got %d: %v", len(result), result)
	}
	if _, ok := result["myproject"]; !ok {
		t.Errorf("expected 'myproject', got %v", result)
	}
}

func TestParseVagrantGlobalStatus_MultipleRunning(t *testing.T) {
	output := `id       name    provider  state    directory
-------------------------------------------------------
abc1234  default utm       started  /Users/dmitry/dev/proj1/.devcell
def5678  default libvirt   running  /home/user/proj2/.devcell
`
	result := runner.ParseVagrantGlobalStatus(output)
	if len(result) != 2 {
		t.Fatalf("want 2 running VMs, got %d: %v", len(result), result)
	}
}

func TestParseVagrantGlobalStatus_Empty(t *testing.T) {
	result := runner.ParseVagrantGlobalStatus("")
	if len(result) != 0 {
		t.Errorf("want empty map for empty output, got %v", result)
	}
}

// L1: VagrantReadForwardedPort — pure file read, no subprocess.

func TestVagrantReadForwardedPort_RDP(t *testing.T) {
	dir := t.TempDir()
	content := `Vagrant.configure("2") do |config|
  config.vm.network "forwarded_port", guest: 5900, host: 40550, id: "vnc"
  config.vm.network "forwarded_port", guest: 3389, host: 36289, id: "rdp"
end
`
	if err := os.WriteFile(dir+"/Vagrantfile", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	port, ok := runner.VagrantReadForwardedPort(dir, "rdp")
	if !ok {
		t.Fatal("expected rdp port to be found")
	}
	if port != "36289" {
		t.Errorf("want 36289, got %q", port)
	}
}

func TestVagrantReadForwardedPort_VNC(t *testing.T) {
	dir := t.TempDir()
	content := `Vagrant.configure("2") do |config|
  config.vm.network "forwarded_port", guest: 5900, host: 40550, id: "vnc"
  config.vm.network "forwarded_port", guest: 3389, host: 36289, id: "rdp"
end
`
	if err := os.WriteFile(dir+"/Vagrantfile", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	port, ok := runner.VagrantReadForwardedPort(dir, "vnc")
	if !ok {
		t.Fatal("expected vnc port to be found")
	}
	if port != "40550" {
		t.Errorf("want 40550, got %q", port)
	}
}

func TestVagrantReadForwardedPort_MissingID(t *testing.T) {
	dir := t.TempDir()
	content := `Vagrant.configure("2") do |config|
  config.vm.network "forwarded_port", guest: 5900, host: 40550, id: "vnc"
end
`
	if err := os.WriteFile(dir+"/Vagrantfile", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok := runner.VagrantReadForwardedPort(dir, "rdp")
	if ok {
		t.Error("expected false when id not present")
	}
}

func TestVagrantReadForwardedPort_NoVagrantfile(t *testing.T) {
	_, ok := runner.VagrantReadForwardedPort(t.TempDir(), "rdp")
	if ok {
		t.Error("expected false when no Vagrantfile")
	}
}

// L1: VagrantMachinePort parsing — pure, tests ParseVagrantMachineReadable directly.

func TestParseVagrantMachineReadable_RDP(t *testing.T) {
	output := `1699999999,,metadata,machine-count,1
1699999999,default,forwarded_port,22,2222
1699999999,default,forwarded_port,3389,36289
1699999999,default,forwarded_port,5900,40550
`
	port, ok := runner.ParseVagrantPortOutput(output, "3389")
	if !ok {
		t.Fatal("expected rdp port to be found")
	}
	if port != "36289" {
		t.Errorf("want 36289, got %q", port)
	}
}

func TestParseVagrantMachineReadable_VNC(t *testing.T) {
	output := `1699999999,default,forwarded_port,22,2222
1699999999,default,forwarded_port,3389,36289
1699999999,default,forwarded_port,5900,40550
`
	port, ok := runner.ParseVagrantPortOutput(output, "5900")
	if !ok {
		t.Fatal("expected vnc port to be found")
	}
	if port != "40550" {
		t.Errorf("want 40550, got %q", port)
	}
}

func TestParseVagrantMachineReadable_MissingPort(t *testing.T) {
	output := `1699999999,default,forwarded_port,22,2222
`
	_, ok := runner.ParseVagrantPortOutput(output, "3389")
	if ok {
		t.Error("expected false when port not in output")
	}
}
