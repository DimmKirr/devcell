package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vagrantHome sets up a temp HOME with a scaffolded config dir and project-level .devcell.toml.
func vagrantHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "devcell")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".devcell.toml"), []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestEngineVagrant_DryRunPrintsArgv checks that --engine=vagrant --dry-run
// prints a vagrant ssh argv and exits 0 (no "not yet implemented").
func TestEngineVagrant_DryRunPrintsArgv(t *testing.T) {
	home := vagrantHome(t)
	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "vagrant") {
		t.Errorf("expected 'vagrant' in dry-run argv output, got:\n%s", s)
	}
	if !strings.Contains(s, "ssh") {
		t.Errorf("expected 'ssh' in dry-run argv output, got:\n%s", s)
	}
	if strings.Contains(strings.ToLower(s), "not yet implemented") {
		t.Errorf("dry-run should not print 'not yet implemented', got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("vagrant engine should not print docker run argv, got:\n%s", s)
	}
}

// TestEngineMacos_AliasForVagrant checks that --macos produces the same vagrant argv.
func TestEngineMacos_AliasForVagrant(t *testing.T) {
	home := vagrantHome(t)
	cmd := exec.Command(binaryPath, "--macos", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 for --macos, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "vagrant") {
		t.Errorf("expected 'vagrant' in --macos dry-run output, got:\n%s", s)
	}
}

// TestEngineVagrant_ScaffoldsLinuxVagrantfile checks that running --engine=vagrant
// creates a Vagrantfile in the project's .devcell/ directory (not global config).
func TestEngineVagrant_ScaffoldsLinuxVagrantfile(t *testing.T) {
	home := vagrantHome(t)

	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}

	if _, err := os.Stat(filepath.Join(home, ".devcell", "Vagrantfile")); err != nil {
		t.Errorf("Vagrantfile not created in .devcell/: %v", err)
	}
}

// TestEngineVagrant_VagrantfileContainsLinuxProvisioner checks the Vagrantfile
// includes the Nix provisioner (linux template, not macOS one).
func TestEngineVagrant_VagrantfileContainsLinuxProvisioner(t *testing.T) {
	home := vagrantHome(t)

	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(home, ".devcell", "Vagrantfile"))
	if err != nil {
		t.Fatalf("Vagrantfile not found: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "nix") {
		t.Errorf("expected Nix provisioner in Vagrantfile, got:\n%s", content)
	}
	if !strings.Contains(content, "home-manager") {
		t.Errorf("expected home-manager in Vagrantfile, got:\n%s", content)
	}
}

// TestEngineVagrant_BoxNameSubstituted checks that --vagrant-box is injected
// into the Vagrantfile.
func TestEngineVagrant_BoxNameSubstituted(t *testing.T) {
	home := vagrantHome(t)

	cmd := exec.Command(binaryPath, "--engine=vagrant", "--vagrant-box=my-test-box", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(home, ".devcell", "Vagrantfile"))
	if err != nil {
		t.Fatalf("Vagrantfile not found: %v", err)
	}
	if !strings.Contains(string(data), "my-test-box") {
		t.Errorf("box name not substituted in Vagrantfile:\n%s", string(data))
	}
}

// TestEngineVagrant_DryRunContainsVagrantDir checks that the dry-run argv
// includes the .devcell/ path (so vagrant knows where to find the Vagrantfile).
func TestEngineVagrant_DryRunContainsVagrantDir(t *testing.T) {
	home := vagrantHome(t)
	buildDir := filepath.Join(home, ".devcell")

	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), buildDir) {
		t.Errorf("expected .devcell dir %q in dry-run argv, got:\n%s", buildDir, string(out))
	}
}
