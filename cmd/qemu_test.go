package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// qemuTestHome sets up a temp HOME with config dir and .devcell.toml for qemu tests.
func qemuTestHome(t *testing.T) string {
	t.Helper()
	return qemuTestHomeWithTOML(t, "[cell]\n")
}

// qemuTestHomeWithTOML sets up a temp HOME with custom project TOML content.
func qemuTestHomeWithTOML(t *testing.T, projectTOML string) string {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "devcell")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".devcell.toml"), []byte(projectTOML), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

// --- Cross-platform smoke tests (dry-run, mock, help) ---

func TestEngineQemu_DryRunPrintsSSH(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "ssh") {
		t.Errorf("expected 'ssh' in dry-run output, got:\n%s", s)
	}
	if !strings.Contains(s, "powershell") {
		t.Errorf("expected 'powershell' in dry-run output, got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("qemu engine should not print docker run argv, got:\n%s", s)
	}
}

func TestEngineQemu_DryRunContainsBinary(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "claude") {
		t.Errorf("expected 'claude' in dry-run output, got:\n%s", out)
	}
}

func TestEngineQemu_DryRunNoDocker(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "claude", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if strings.Contains(string(out), "docker") {
		t.Errorf("qemu engine should not involve docker, got:\n%s", out)
	}
}

func TestEngineQemu_DryRunContainsEnvVars(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home, "TERM=xterm-256color")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "TERM=") {
		t.Errorf("expected TERM= in dry-run output, got:\n%s", out)
	}
}

func TestEngineQemu_DryRunSSHPort(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
	cmd.Dir = home
	// DEVCELL_BUNK=1, no SESSION_PORT_PREFIX → portPrefix="1" → SSH=ClampPort("122")=122 → hoisted to 10122
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "-p 10122") {
		t.Errorf("expected '-p 10122' (bunk-based SSH port) in dry-run output, got:\n%s", out)
	}
}

func TestEngineQemu_DryRunCustomSSHPort(t *testing.T) {
	home := qemuTestHomeWithTOML(t, "[cell]\nqemu_ssh_port = 3333\n")
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "-p 3333") {
		t.Errorf("expected '-p 3333' (custom SSH port) in dry-run output, got:\n%s", out)
	}
}

func TestEngineQemu_EngineHelpIncludesQemu(t *testing.T) {
	out, err := exec.Command(binaryPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "qemu") {
		t.Errorf("expected 'qemu' in --help output for --engine flag, got:\n%s", out)
	}
}

func TestBackgroundFlag_StrippedFromArgsQemu(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "--background", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "--background") {
		t.Errorf("--background should be stripped from forwarded args, got:\n%s", s)
	}
}

// --- Non-darwin tests ---

func TestEngineQemu_NoDebugOnLinux(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this test validates the non-darwin error path")
	}
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error on non-darwin without --debug, got exit 0:\n%s", out)
	}
	s := string(out)
	if !strings.Contains(s, "qemu engine requires macOS") {
		t.Errorf("expected 'qemu engine requires macOS' in error, got:\n%s", s)
	}
	if !strings.Contains(s, "--debug to simulate") {
		t.Errorf("expected '--debug to simulate' hint in error, got:\n%s", s)
	}
}

func TestEngineQemu_DebugMockOutput(t *testing.T) {
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "--debug", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 with --debug mock, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if runtime.GOOS == "darwin" {
		t.Skip("mock output only on non-darwin")
	}
	for _, want := range []string{
		"[MOCK",
		"mock mode",
		"would exec",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in debug mock output, got:\n%s", want, s)
		}
	}
}

func TestEngineQemu_DebugMockNoDocker(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("mock output only on non-darwin")
	}
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "--debug", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	if strings.Contains(string(out), "docker") {
		t.Errorf("mock output should not mention docker, got:\n%s", out)
	}
}

// --- macOS-only E2E lifecycle tests ---
//
// These tests exercise the full QEMU Windows VM lifecycle on macOS Apple Silicon.
// They require:
//   - darwin/arm64 runtime
//   - qemu-system-aarch64 installed (brew install qemu)
//   - DEVCELL_QEMU_WINDOWS_ISO set to a valid Windows 11 ARM64 ISO path
//
// Run modes:
//   go test ./cmd -run TestQemuE2E                     → skips (short mode)
//   go test ./cmd -run TestQemuE2E -count=1 -timeout=0 → full lifecycle (~45 min)
//
// The subtests are ordered and share state via a temp HOME directory:
//   Init  → creates SSH keys + downloads VirtIO drivers (~2 min)
//   Build → installs Windows + provisions (~30-45 min)
//   Shell → verifies instance clone + dry-run SSH command

func TestQemuE2E_FullLifecycle(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("QEMU E2E requires macOS on Apple Silicon (darwin/arm64)")
	}
	if testing.Short() {
		t.Skip("long: QEMU E2E lifecycle takes ~45 min — run with -count=1 -timeout=0")
	}

	// Check prerequisites
	qemuBin, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		t.Skip("qemu-system-aarch64 not found — install with: brew install qemu")
	}
	t.Logf("QEMU binary: %s", qemuBin)

	windowsISO := os.Getenv("DEVCELL_QEMU_WINDOWS_ISO")
	if windowsISO == "" {
		t.Skip("DEVCELL_QEMU_WINDOWS_ISO not set — download from https://www.microsoft.com/en-us/software-download/windows11arm64")
	}
	if _, err := os.Stat(windowsISO); err != nil {
		t.Fatalf("Windows ISO not found at %s: %v", windowsISO, err)
	}
	t.Logf("Windows ISO: %s", windowsISO)

	// Set up isolated HOME
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "devcell")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte("[cell]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".devcell.toml"), []byte("[cell]\nstack = \"base\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Test HOME: %s", home)

	cellName := "test-qemu-e2e"
	baseEnv := append(os.Environ(),
		"DEVCELL_BUNK=1",
		"HOME="+home,
		"DEVCELL_CELL_NAME="+cellName,
		"DEVCELL_QEMU_WINDOWS_ISO="+windowsISO,
	)

	// --- Phase 1: Init ---
	t.Run("Init", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "--engine=qemu", "--debug", "init", "--stack=base")
		cmd.Dir = home
		cmd.Env = baseEnv
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("cell init --engine=qemu failed: %v", err)
		}

		// Verify SSH keys were created
		sshDir := filepath.Join(home, ".devcell", cellName, "qemu")
		for _, f := range []string{"id_ed25519", "id_ed25519.pub", "authorized_keys"} {
			path := filepath.Join(sshDir, f)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("expected SSH file %s to exist: %v", f, err)
			}
		}

		// Verify VirtIO drivers were downloaded
		virtioPath := filepath.Join(home, ".devcell", "cache", "qemu", "virtio-win.iso")
		if info, err := os.Stat(virtioPath); err != nil {
			t.Errorf("VirtIO ISO not found at %s: %v", virtioPath, err)
		} else {
			t.Logf("VirtIO ISO: %s (%.0f MB)", virtioPath, float64(info.Size())/(1024*1024))
		}

		donePath := virtioPath + ".done"
		if _, err := os.Stat(donePath); err != nil {
			t.Errorf("VirtIO .done marker not found: %v", err)
		}

		// Verify directories were created
		templateDir := filepath.Join(home, ".devcell", "windows", "base")
		if _, err := os.Stat(templateDir); err != nil {
			t.Errorf("template dir not created: %v", err)
		}

		instanceDir := filepath.Join(home, ".devcell", cellName, "windows")
		if _, err := os.Stat(instanceDir); err != nil {
			t.Errorf("instance dir not created: %v", err)
		}
	})

	// --- Phase 2: Build ---
	t.Run("Build", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "--engine=qemu", "--debug", "build", "--stack=base")
		cmd.Dir = home
		cmd.Env = baseEnv
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("cell build --engine=qemu failed: %v", err)
		}

		// Verify template disk was created
		templateDisk := filepath.Join(home, ".devcell", "windows", "base", "disk-base.qcow2")
		if info, err := os.Stat(templateDisk); err != nil {
			t.Errorf("template disk not found: %v", err)
		} else {
			t.Logf("Template disk: %s (%.1f GB)", templateDisk, float64(info.Size())/(1024*1024*1024))
		}

		// Verify UEFI vars file
		varsPath := filepath.Join(home, ".devcell", "windows", "base", "vars.fd")
		if _, err := os.Stat(varsPath); err != nil {
			t.Errorf("UEFI vars file not found: %v", err)
		}

		// Verify provisioned marker
		marker := filepath.Join(home, ".devcell", "windows", "base", ".provisioned")
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("provisioned marker not found: %v", err)
		}
	})

	// --- Phase 3: Shell (dry-run — verifies clone + SSH command) ---
	t.Run("ShellDryRun", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
		cmd.Dir = home
		cmd.Env = baseEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cell shell --engine=qemu --dry-run failed: %v\noutput: %s", err, out)
		}

		s := string(out)
		// Should print SSH command
		if !strings.Contains(s, "ssh") {
			t.Errorf("expected 'ssh' in shell dry-run output, got:\n%s", s)
		}
		if !strings.Contains(s, "powershell") {
			t.Errorf("expected 'powershell' in shell dry-run output, got:\n%s", s)
		}
		// DEVCELL_BUNK=1 → bunk-based SSH port (10122)
		if !strings.Contains(s, "-p 10122") {
			t.Errorf("expected '-p 10122' (bunk-based SSH port) in shell dry-run output, got:\n%s", s)
		}
		t.Logf("Shell dry-run output:\n%s", s)
	})
}

// TestQemuE2E_InitOnly exercises just the init phase — useful for quick macOS
// validation without the 45-minute build. Downloads VirtIO drivers (~500MB)
// and generates SSH keys.
//
// Run: go test ./cmd -run TestQemuE2E_InitOnly -count=1 -timeout=10m
func TestQemuE2E_InitOnly(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("QEMU E2E requires macOS on Apple Silicon (darwin/arm64)")
	}
	if testing.Short() {
		t.Skip("long: downloads VirtIO drivers (~500MB)")
	}
	if _, err := exec.LookPath("qemu-system-aarch64"); err != nil {
		t.Skip("qemu-system-aarch64 not found — install with: brew install qemu")
	}

	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "devcell")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "devcell.toml"), []byte("[cell]\n"), 0644)
	os.WriteFile(filepath.Join(home, ".devcell.toml"), []byte("[cell]\n"), 0644)

	cellName := "test-qemu-init"
	cmd := exec.Command(binaryPath, "--engine=qemu", "--debug", "init", "--stack=base")
	cmd.Dir = home
	cmd.Env = append(os.Environ(),
		"DEVCELL_BUNK=1",
		"HOME="+home,
		"DEVCELL_CELL_NAME="+cellName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("cell init --engine=qemu failed: %v", err)
	}

	// Verify SSH keypair
	sshDir := filepath.Join(home, ".devcell", cellName, "qemu")
	privKey := filepath.Join(sshDir, "id_ed25519")
	pubKey := filepath.Join(sshDir, "id_ed25519.pub")
	authKeys := filepath.Join(sshDir, "authorized_keys")

	for _, path := range []string{privKey, pubKey, authKeys} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", filepath.Base(path), err)
		}
	}

	// Verify key content looks like ed25519
	pubKeyData, err := os.ReadFile(pubKey)
	if err != nil {
		t.Fatalf("reading pub key: %v", err)
	}
	if !strings.HasPrefix(string(pubKeyData), "ssh-ed25519 ") {
		t.Errorf("pub key should start with 'ssh-ed25519', got: %s", string(pubKeyData)[:40])
	}

	// Verify VirtIO ISO downloaded
	virtioPath := filepath.Join(home, ".devcell", "cache", "qemu", "virtio-win.iso")
	info, err := os.Stat(virtioPath)
	if err != nil {
		t.Fatalf("VirtIO ISO not found: %v", err)
	}
	if info.Size() < 100*1024*1024 {
		t.Errorf("VirtIO ISO suspiciously small: %d bytes", info.Size())
	}
	t.Logf("VirtIO ISO downloaded: %.0f MB", float64(info.Size())/(1024*1024))

	// Verify .done marker
	if _, err := os.Stat(virtioPath + ".done"); err != nil {
		t.Errorf(".done marker not found: %v", err)
	}

	// Verify idempotency — second run should be fast (cache hit)
	cmd2 := exec.Command(binaryPath, "--engine=qemu", "--debug", "init", "--stack=base")
	cmd2.Dir = home
	cmd2.Env = append(os.Environ(),
		"DEVCELL_BUNK=1",
		"HOME="+home,
		"DEVCELL_CELL_NAME="+cellName,
	)
	out, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("second init failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "cache hit") {
		t.Logf("second init output (expected cache hit):\n%s", out)
	}
}

func TestEngineQemu_BuildUseBunkPorts(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("build --engine=qemu requires darwin/arm64")
	}
	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "--debug", "build", "--stack=base")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=3", "HOME="+home)
	out, err := cmd.CombinedOutput()
	s := string(out)
	_ = err
	// DEVCELL_BUNK=3, no SESSION_PORT_PREFIX → prefix "3" → SSH=ClampPort("322")=322 → hoisted to 10322
	if !strings.Contains(s, "10322") {
		t.Errorf("expected bunk-based SSH port 10322 in build debug output, got:\n%s", s)
	}
	if strings.Contains(s, "2222") {
		t.Errorf("build should not use hardcoded SSH port 2222, got:\n%s", s)
	}
}

// TestQemuE2E_BuildDryRun verifies that --dry-run prints what would be built
// without actually starting QEMU.
func TestQemuE2E_BuildDryRun(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("QEMU build requires macOS on Apple Silicon (darwin/arm64)")
	}

	home := qemuTestHome(t)
	cmd := exec.Command(binaryPath, "--engine=qemu", "build", "--dry-run", "--stack=base")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "Would build") {
		t.Errorf("expected 'Would build' in --dry-run output, got:\n%s", s)
	}
	if !strings.Contains(s, "base") {
		t.Errorf("expected 'base' stack in --dry-run output, got:\n%s", s)
	}
}
