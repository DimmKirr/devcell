package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestThin_DefaultMountsVolume verifies thin is the default (volume mount present).
func TestThin_DefaultMountsVolume(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "devcell-nix-store:/nix") {
		t.Errorf("default (thin) should have nix store volume mount:\n%s", argv)
	}
}

// TestThin_DefaultUsesThinTag verifies default selects the -thin tagged image.
func TestThin_DefaultUsesThinTag(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "-thin") {
		t.Errorf("default should select -thin image tag:\n%s", argv)
	}
}

// TestThin_ExplicitFlagMountsVolume verifies --thin still works explicitly.
func TestThin_ExplicitFlagMountsVolume(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--thin", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --thin --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "devcell-nix-store:/nix") {
		t.Errorf("--thin should add volume mount:\n%s", argv)
	}
}

// TestThin_ThickDisablesVolume verifies --thick removes the volume mount.
func TestThin_ThickDisablesVolume(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--thick", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --thick --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "devcell-nix-store") {
		t.Errorf("--thick should NOT have nix store volume:\n%s", argv)
	}
}

// TestThin_NoThinDisablesVolume verifies --no-thin removes the volume mount.
func TestThin_NoThinDisablesVolume(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--no-thin", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --no-thin --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "devcell-nix-store") {
		t.Errorf("--no-thin should NOT have nix store volume:\n%s", argv)
	}
}

// TestThin_EnvVarDisables verifies DEVCELL_THIN=0 disables thin mode.
func TestThin_EnvVarDisables(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home, "DEVCELL_THIN=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run with DEVCELL_THIN=0 failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "devcell-nix-store") {
		t.Errorf("DEVCELL_THIN=0 should disable volume mount:\n%s", argv)
	}
}

// TestThin_EnvVarEnables verifies DEVCELL_THIN=1 enables thin mode.
func TestThin_EnvVarEnables(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home, "DEVCELL_THIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run with DEVCELL_THIN=1 failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "devcell-nix-store:/nix") {
		t.Errorf("DEVCELL_THIN=1 should enable volume mount:\n%s", argv)
	}
}

// TestThin_TomlFalseDisables verifies [cell] thin=false disables thin mode.
func TestThin_TomlFalseDisables(t *testing.T) {
	home := scaffoldedHome(t)

	cfgDir := filepath.Join(home, ".config", "devcell")
	tomlContent := `[cell]
thin = false
`
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --dry-run with thin=false toml failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if strings.Contains(argv, "devcell-nix-store") {
		t.Errorf("[cell] thin=false should disable volume mount:\n%s", argv)
	}
}

// TestThin_FlagStripped verifies --thin is NOT forwarded to the inner binary.
func TestThin_FlagStripped(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--thin", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --thin --dry-run failed: %v\noutput: %s", err, out)
	}

	for _, f := range strings.Fields(string(out)) {
		if f == "--thin" {
			t.Errorf("--thin should be stripped from argv:\n%s", string(out))
		}
	}
}

// TestThin_ThickFlagStripped verifies --thick is NOT forwarded to the inner binary.
func TestThin_ThickFlagStripped(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "claude", "--thick", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude --thick --dry-run failed: %v\noutput: %s", err, out)
	}

	for _, f := range strings.Fields(string(out)) {
		if f == "--thick" || f == "--no-thin" {
			t.Errorf("--thick/--no-thin should be stripped from argv:\n%s", string(out))
		}
	}
}

// TestThin_Shell verifies default thin works with shell subcommand.
func TestThin_Shell(t *testing.T) {
	home := scaffoldedHome(t)

	cmd := exec.Command(binaryPath, "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell --dry-run failed: %v\noutput: %s", err, out)
	}

	argv := string(out)
	if !strings.Contains(argv, "devcell-nix-store:/nix") {
		t.Errorf("default thin should add volume mount for shell:\n%s", argv)
	}
}
