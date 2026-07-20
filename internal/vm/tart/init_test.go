package tart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitConfig_Defaults(t *testing.T) {
	c := InitConfig{HomeDir: "/home/test"}
	c.ApplyDefaults()

	if c.Username != "admin" {
		t.Errorf("Username = %q, want admin (Cirrus Labs default)", c.Username)
	}
	if c.Password != "admin" {
		t.Errorf("Password = %q, want admin (Cirrus Labs default)", c.Password)
	}
	if c.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", c.CPUs)
	}
	if c.MemoryGB != 4 {
		t.Errorf("MemoryGB = %d, want 4", c.MemoryGB)
	}
	if c.DiskGB != 64 {
		t.Errorf("DiskGB = %d, want 64", c.DiskGB)
	}
	if c.SSHPort != 22 {
		t.Errorf("SSHPort = %d, want 22", c.SSHPort)
	}
	if c.Stack != "base" {
		t.Errorf("Stack = %q, want base", c.Stack)
	}
	if c.CellName != "main" {
		t.Errorf("CellName = %q, want main", c.CellName)
	}
}

func TestInitConfig_Validate(t *testing.T) {
	c := InitConfig{CellName: "test"}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing HomeDir")
	}

	c = InitConfig{HomeDir: "/home/test"}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing CellName")
	}

	c = InitConfig{HomeDir: "/home/test", CellName: "main"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInitConfig_DiskSizeBytes(t *testing.T) {
	c := InitConfig{DiskGB: 64}
	want := int64(64 * 1024 * 1024 * 1024)
	if got := c.DiskSizeBytes(); got != want {
		t.Errorf("DiskSizeBytes = %d, want %d", got, want)
	}
}

func TestInitConfig_ArtifactDir(t *testing.T) {
	c := InitConfig{HomeDir: "/Users/test", CellName: "dev"}
	want := "/Users/test/.devcell/dev/darwin"
	if got := c.ArtifactDir(); got != want {
		t.Errorf("ArtifactDir = %q, want %q", got, want)
	}
}

func TestInitPreflight_NotDarwin(t *testing.T) {
	_, err := InitPreflight("linux", "amd64", "/tmp/nonexistent")
	if err == nil {
		t.Fatal("expected error on linux")
	}
	if !strings.Contains(err.Error(), "macOS") {
		t.Errorf("error %q should mention macOS", err)
	}
}

func TestInitPreflight_VMExists(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "disk.img"), []byte("x"), 0644)

	result, err := InitPreflight("darwin", "arm64", dir)
	if err != nil {
		t.Fatalf("InitPreflight should not error when VM exists, got: %v", err)
	}
	if !result.VMExists {
		t.Error("VMExists should be true when disk.img exists")
	}
}

func TestInitPreflight_NoVM(t *testing.T) {
	dir := t.TempDir()
	result, err := InitPreflight("darwin", "arm64", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.VMExists {
		t.Error("VMExists should be false when disk.img is absent")
	}
}

func TestInitPhase_String(t *testing.T) {
	if PhasePreflight.String() != "preflight" {
		t.Errorf("PhasePreflight.String() = %q", PhasePreflight.String())
	}
	if PhaseInstallNix.String() != "install-nix" {
		t.Errorf("PhaseInstallNix.String() = %q", PhaseInstallNix.String())
	}
	if PhaseMountNixhome.String() != "mount-nixhome" {
		t.Errorf("PhaseMountNixhome.String() = %q", PhaseMountNixhome.String())
	}
	if PhaseActivateDarwin.String() != "activate-darwin" {
		t.Errorf("PhaseActivateDarwin.String() = %q", PhaseActivateDarwin.String())
	}
}

func TestGenerateSSHKeyPair(t *testing.T) {
	dir := t.TempDir()
	pubKey, err := GenerateSSHKeyPair(dir)
	if err != nil {
		t.Fatalf("GenerateSSHKeyPair: %v", err)
	}

	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Errorf("pubKey should start with 'ssh-ed25519 ', got %q", pubKey[:30])
	}

	privPath := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(privPath); err != nil {
		t.Errorf("private key not found: %v", err)
	}

	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if _, err := os.Stat(pubPath); err != nil {
		t.Errorf("public key not found: %v", err)
	}

	// Check file permissions
	info, _ := os.Stat(privPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("private key permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestProvisionCommands_ContainsAllPhases(t *testing.T) {
	cfg := InitConfig{Username: "devcell", Stack: "ultimate"}
	cmds := ProvisionCommands(cfg, "ssh-ed25519 AAAA...")

	if len(cmds) != 6 {
		t.Fatalf("ProvisionCommands returned %d commands, want 6", len(cmds))
	}

	// SSH enablement
	if !strings.Contains(cmds[0], "setremotelogin") {
		t.Errorf("cmd[0] should enable SSH: %q", cmds[0])
	}
	// SSH key injection
	if !strings.Contains(cmds[1], "authorized_keys") {
		t.Errorf("cmd[1] should inject SSH key: %q", cmds[1])
	}
	// Sudoers
	if !strings.Contains(cmds[2], "sudoers") {
		t.Errorf("cmd[2] should configure sudo: %q", cmds[2])
	}
	// Official Nix install
	if !strings.Contains(cmds[3], "nixos.org/nix/install") {
		t.Errorf("cmd[3] should install Nix via official installer: %q", cmds[3])
	}
	// VirtioFS mount
	if !strings.Contains(cmds[4], "mount_virtiofs") {
		t.Errorf("cmd[4] should mount nixhome via VirtioFS: %q", cmds[4])
	}
	// nix-darwin activate
	if !strings.Contains(cmds[5], "nix-darwin") {
		t.Errorf("cmd[5] should activate nix-darwin with stack 'ultimate': %q", cmds[5])
	}
}
