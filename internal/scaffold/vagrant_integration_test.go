//go:build integration

package scaffold_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
)

// nixhomeDir returns the absolute path to the nixhome directory in the devcell repo.
func nixhomeDir(t *testing.T) string {
	t.Helper()
	// Walk up from this file's directory to find nixhome/
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine source file path")
	}
	// internal/scaffold/vagrant_integration_test.go → repo root → nixhome
	root := filepath.Join(filepath.Dir(file), "..", "..")
	nixhome := filepath.Clean(filepath.Join(root, "nixhome"))
	if _, err := os.Stat(nixhome); err != nil {
		t.Fatalf("nixhome not found at %s: %v", nixhome, err)
	}
	return nixhome
}

// vagrantCmd runs a vagrant command in dir, streaming output and enforcing timeout.
func vagrantCmd(t *testing.T, dir string, timeout time.Duration, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vagrant", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("vagrant %s: %v", strings.Join(args, " "), err)
	}
}

// vagrantSSH runs a command inside the VM via vagrant ssh and returns output.
func vagrantSSH(t *testing.T, dir, command string) string {
	t.Helper()
	out, err := exec.Command("vagrant", "ssh", "--", command).Output()
	if err != nil {
		t.Fatalf("vagrant ssh %q: %v", command, err)
	}
	return strings.TrimSpace(string(out))
}

// TestVagrantNixDarwin is an end-to-end integration test for the macOS VM path:
//  1. Scaffold a Vagrantfile (with NIXHOME_PATH pointing to the local nixhome)
//  2. vagrant up (starts the UTM VM)
//  3. vagrant provision --provision-with nix-install
//  4. vagrant provision --provision-with nix-darwin
//  5. Assert darwin-rebuild is functional
//  6. Assert at least one package from the devcell base profile is present
//
// Run with: go test -tags integration -v -timeout 30m ./internal/scaffold/...
func TestVagrantNixDarwin(t *testing.T) {
	if os.Getenv("MACOS_BOX") == "" {
		t.Skip("MACOS_BOX not set — skipping Vagrant integration test")
	}

	nixhome := nixhomeDir(t)
	dir := t.TempDir()

	// 1. Scaffold Vagrantfile — box name from env, nixhome path embedded directly
	boxName := os.Getenv("MACOS_BOX")
	if err := scaffold.ScaffoldVagrantfile(dir, boxName, nixhome); err != nil {
		t.Fatalf("ScaffoldVagrantfile: %v", err)
	}
	vagrantfile := filepath.Join(dir, "Vagrantfile")
	data, _ := os.ReadFile(vagrantfile)
	if !strings.Contains(string(data), boxName) {
		t.Fatalf("Vagrantfile missing box name %q", boxName)
	}
	if !strings.Contains(string(data), nixhome) {
		t.Fatalf("Vagrantfile missing nixhome path %q", nixhome)
	}

	// 2. Start VM
	t.Log("Starting VM...")
	vagrantCmd(t, dir, 10*time.Minute, "up", "--no-provision")

	// Ensure we destroy on test exit
	t.Cleanup(func() {
		exec.Command("vagrant", "destroy", "-f").Run()
	})

	// 3. Install Nix
	t.Log("Installing Nix...")
	vagrantCmd(t, dir, 10*time.Minute, "provision", "--provision-with", "nix-install")

	// 4. Apply nix-darwin
	t.Log("Applying nix-darwin...")
	vagrantCmd(t, dir, 20*time.Minute, "provision", "--provision-with", "nix-darwin")

	// 5. darwin-rebuild must be present
	out := vagrantSSH(t, dir, "darwin-rebuild --version 2>&1 || echo MISSING")
	if strings.Contains(out, "MISSING") {
		t.Errorf("darwin-rebuild not found after provisioning; output: %s", out)
	}
	t.Logf("darwin-rebuild: %s", out)

	// 6. At least one package from the base profile must be on PATH
	// git is in base.nix home.packages
	out = vagrantSSH(t, dir, "which git")
	if out == "" {
		t.Error("git not found on PATH — nix-darwin profile may not have activated")
	}
	t.Logf("git: %s", out)
}
