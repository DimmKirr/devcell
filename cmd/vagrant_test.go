package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vagrantHome sets up a temp HOME with a scaffolded config dir.
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
	return home
}

// TestEngineVagrant_PrintsStubWarning checks that --engine=vagrant prints a
// "not yet implemented" warning and exits 0 without printing docker argv.
func TestEngineVagrant_PrintsStubWarning(t *testing.T) {
	home := vagrantHome(t)
	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 for vagrant stub, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(strings.ToLower(s), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in output, got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("vagrant stub should not print docker run argv, got:\n%s", s)
	}
}

// TestEngineMacos_AliasForVagrant checks that --macos produces the same stub.
func TestEngineMacos_AliasForVagrant(t *testing.T) {
	home := vagrantHome(t)
	cmd := exec.Command(binaryPath, "--macos", "shell", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 for --macos stub, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(strings.ToLower(s), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in output, got:\n%s", s)
	}
}

// TestEngineVagrant_ScaffoldsVagrantfile checks that running --engine=vagrant
// creates a Vagrantfile in the config directory.
func TestEngineVagrant_ScaffoldsVagrantfile(t *testing.T) {
	home := vagrantHome(t)
	cfgDir := filepath.Join(home, ".config", "devcell")

	cmd := exec.Command(binaryPath, "--engine=vagrant", "shell", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0 for vagrant stub, got: %v\noutput: %s", err, out)
	}

	if _, err := os.Stat(filepath.Join(cfgDir, "Vagrantfile")); err != nil {
		t.Errorf("Vagrantfile not created in config dir: %v", err)
	}
}

// TestEngineVagrant_BoxNameSubstituted checks that --vagrant-box is injected
// into the Vagrantfile.
func TestEngineVagrant_BoxNameSubstituted(t *testing.T) {
	home := vagrantHome(t)
	cfgDir := filepath.Join(home, ".config", "devcell")

	cmd := exec.Command(binaryPath, "--engine=vagrant", "--vagrant-box=my-test-box", "shell", "--dry-run")
	cmd.Env = append(os.Environ(), "CELL_ID=1", "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0 for vagrant stub, got: %v\noutput: %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "Vagrantfile"))
	if err != nil {
		t.Fatalf("Vagrantfile not found: %v", err)
	}
	if !strings.Contains(string(data), "my-test-box") {
		t.Errorf("box name not substituted in Vagrantfile:\n%s", string(data))
	}
}
