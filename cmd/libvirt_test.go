package main_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- libvirt engine dispatch (CELL-372) ---
//
// The libvirt branch is plumbing-first: `--engine=libvirt` (or `[cell]
// engine = "libvirt"`) must reach a libvirt-specific path instead of the
// docker runner. Until CELL-377 lands the non-dry-run path returns a clear
// "not implemented" error; --dry-run prints the resolved URI.

func libvirtTestHome(t *testing.T, projectTOML string) string {
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

func TestEngineLibvirt_DryRunPrintsDefaultURI(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "--engine=libvirt", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 in dry-run, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "qemu+tcp://host.docker.internal/session") {
		t.Errorf("expected default libvirt URI in dry-run output, got:\n%s", s)
	}
	if !strings.Contains(s, "<domain") {
		t.Errorf("dry-run must print the domain XML it would define, got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("libvirt engine must not print docker run argv, got:\n%s", s)
	}
}

func TestEngineLibvirt_TOMLEngineSelectsLibvirt(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\nengine = \"libvirt\"\n")
	cmd := exec.Command(binaryPath, "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 in dry-run, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "qemu+tcp://host.docker.internal/session") {
		t.Errorf("expected libvirt URI when engine set via TOML, got:\n%s", s)
	}
	if strings.Contains(s, "docker run") {
		t.Errorf("TOML engine=libvirt must not fall through to docker, got:\n%s", s)
	}
}

func TestEngineLibvirt_URIFromTOML(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\nlibvirt_uri = \"qemu+tcp://10.9.9.9/system\"\n")
	cmd := exec.Command(binaryPath, "--engine=libvirt", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 in dry-run, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "qemu+tcp://10.9.9.9/system") {
		t.Errorf("expected TOML libvirt_uri in dry-run output, got:\n%s", out)
	}
}

func TestEngineLibvirt_URIFromEnv(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\nlibvirt_uri = \"qemu+tcp://tomlhost/session\"\n")
	cmd := exec.Command(binaryPath, "--engine=libvirt", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home,
		"DEVCELL_LIBVIRT_URI=qemu+tcp://envhost/session")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 in dry-run, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "qemu+tcp://envhost/session") {
		t.Errorf("expected env URI to win over TOML, got:\n%s", out)
	}
}

func TestEngineLibvirt_RunFailsWithActionablePreflight(t *testing.T) {
	// Pin the URI to a loopback port nothing listens on: the preflight must
	// fail deterministically with the remediation text, regardless of
	// whether the developer's host actually runs libvirtd.
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "--engine=libvirt", "shell")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home,
		"DEVCELL_LIBVIRT_URI=qemu+tcp://127.0.0.1:1/session")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit against dead libvirtd, output: %s", out)
	}
	s := string(out)
	if !strings.Contains(s, "libvirtd") {
		t.Errorf("preflight failure must name libvirtd with remediation, got:\n%s", s)
	}
}

// --- auto-default (CELL-378) ---

// autoDefaultEnvActive mirrors the production probes: only in a Docker cell
// on a Mac (dockerenv + host gateway + no usable kvm) does the upgrade fire.
func autoDefaultEnvActive(t *testing.T) bool {
	t.Helper()
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return false
	}
	if _, err := net.LookupHost("host.docker.internal"); err != nil {
		return false
	}
	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err == nil {
		f.Close()
		return false
	}
	return true
}

func TestEngineQemu_AutoDefaultsToLibvirtInDockerOnMac(t *testing.T) {
	if !autoDefaultEnvActive(t) {
		t.Skip("auto-default probes are not all positive in this environment")
	}
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "--engine=qemu", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "<domain") {
		t.Errorf("qemu in docker-on-mac must upgrade to libvirt remote mode, got:\n%s", s)
	}
	if !strings.Contains(s, "qemu+tcp://host.docker.internal/session") {
		t.Errorf("upgraded run must target the host libvirtd, got:\n%s", s)
	}
}

func TestEngineQemu_LocalFlagPinsLocalQemu(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "--engine=qemu", "--local", "shell", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "<domain") {
		t.Errorf("--local must pin the in-container qemu path, got:\n%s", s)
	}
	if !strings.Contains(s, "powershell") {
		t.Errorf("--local qemu dry-run must print the ssh argv, got:\n%s", s)
	}
	if strings.Contains(s, "--local") {
		t.Errorf("--local must be stripped from forwarded args, got:\n%s", s)
	}
}

func TestEngineLibvirt_BuildDryRun(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "build", "--engine=libvirt", "--dry-run")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 in dry-run, got: %v\noutput: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "libvirt") {
		t.Errorf("expected libvirt mentioned in build dry-run, got:\n%s", s)
	}
	// The default URI contains "host.docker.internal", so ban docker-path
	// markers rather than the bare word.
	for _, marker := range []string{"docker build", "docker run", "Dockerfile"} {
		if strings.Contains(s, marker) {
			t.Errorf("build --engine=libvirt must not reach docker path (%q found), got:\n%s", marker, s)
		}
	}
}

func TestEngineLibvirt_InitDispatches(t *testing.T) {
	home := libvirtTestHome(t, "[cell]\n")
	cmd := exec.Command(binaryPath, "init", "--engine=libvirt")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DEVCELL_BUNK=1", "HOME="+home)
	out, _ := cmd.CombinedOutput()
	// Init reuses the qemu scaffold (keys/dirs are just files); on platforms
	// where that is stubbed out the error must still name libvirt/qemu, and
	// it must never fall through to the docker scaffold path.
	if strings.Contains(string(out), "docker build") || strings.Contains(string(out), "Dockerfile") {
		t.Errorf("init --engine=libvirt must not reach docker scaffold path, got:\n%s", out)
	}
	if !strings.Contains(string(out), "qemu") && !strings.Contains(string(out), "libvirt") {
		t.Errorf("expected libvirt/qemu init path to be exercised, got:\n%s", out)
	}
}
