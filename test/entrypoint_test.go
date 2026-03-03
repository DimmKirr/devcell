package container_test

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// gitShortSHA returns the abbreviated commit hash of HEAD.
func gitShortSHA(t *testing.T) string {
	t.Helper()
	out, err := osexec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// buildTestBaseImage builds the base image via docker buildx bake with a
// unique test-specific tag. Returns the tag. Removes the image on cleanup.
func buildTestBaseImage(t *testing.T) string {
	t.Helper()
	tag := fmt.Sprintf("devcell-test-base:%s-%s", gitShortSHA(t), time.Now().Format("20060102T150405"))
	t.Logf("Building base image: %s", tag)

	cmd := osexec.Command("docker", "buildx", "bake",
		"--file", "docker-bake.hcl",
		"--load",
		"--set", fmt.Sprintf("local-base.tags=%s", tag),
		"local")
	cmd.Dir = filepath.Join("..")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build base image: %v", err)
	}
	t.Cleanup(func() { osexec.Command("docker", "rmi", tag).Run() })
	return tag
}

// buildTestUserImage builds a user image from a scaffolded config directory.
// Returns the tag. Removes the image on cleanup.
func buildTestUserImage(t *testing.T, configDir string) string {
	t.Helper()
	tag := fmt.Sprintf("devcell-test-user:%s-%s", gitShortSHA(t), time.Now().Format("20060102T150405"))
	t.Logf("Building user image: %s (from %s)", tag, configDir)

	cmd := osexec.Command("docker", "build", "-t", tag, configDir)
	cmd.Dir = filepath.Join("..")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build user image: %v", err)
	}
	t.Cleanup(func() { osexec.Command("docker", "rmi", tag).Run() })
	return tag
}

// TestEntrypointFragments is an e2e test that verifies the full
// scaffold → build → run flow for nix-generated entrypoint fragments.
//
// The test:
//  1. Builds (or reuses) a base image with the entrypoint.d sourcing loop
//  2. Scaffolds a config dir using that base image
//  3. Builds a user image (home-manager switch stages 50-gui.sh)
//  4. Starts a container with DEVCELL_GUI_ENABLED=true
//  5. Verifies 50-gui.sh is staged and GUI services are running
//
// Set DEVCELL_TEST_BASE_IMAGE to skip the base image build (CI pre-builds it).
// Skipped in -short mode.
func TestEntrypointFragments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// 1. Resolve base image.
	baseImage := os.Getenv("DEVCELL_TEST_BASE_IMAGE")
	if baseImage == "" {
		baseImage = buildTestBaseImage(t)
	} else {
		t.Logf("Using pre-built base image: %s", baseImage)
	}

	// 2. Scaffold config dir with this base image.
	configDir := t.TempDir()
	t.Setenv("DEVCELL_BASE_IMAGE", baseImage)
	if err := scaffold.Scaffold(configDir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Verify Dockerfile FROM line.
	dockerfile, err := os.ReadFile(filepath.Join(configDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.HasPrefix(string(dockerfile), "FROM "+baseImage) {
		t.Fatalf("Dockerfile FROM doesn't match base image: got %.80s", string(dockerfile))
	}
	t.Logf("Scaffold OK: Dockerfile FROM %s", baseImage)

	// 3. Build user image.
	userImage := buildTestUserImage(t, configDir)

	// 4. Start container with GUI enabled, wait for xrdp to listen on 3389.
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        userImage,
		ExposedPorts: []string{"3389/tcp", "5900/tcp"},
		Env: map[string]string{
			"HOST_USER":           hostUser,
			"APP_NAME":            "test",
			"DEVCELL_GUI_ENABLED": "true",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"sh", "-c",
			"grep -qi 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null && grep -qi ' 0A ' /proc/net/tcp6 /proc/net/tcp 2>/dev/null"}).
			WithStartupTimeout(120 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	// 5. Verify entrypoint fragments and GUI services.
	t.Run("fragment_staged", func(t *testing.T) {
		out, code := exec(t, c, []string{"ls", "-la", "/etc/devcell/entrypoint.d/50-gui.sh"})
		if code != 0 {
			t.Fatalf("FAIL: 50-gui.sh not found in /etc/devcell/entrypoint.d/ (exit %d)", code)
		}
		if !strings.Contains(out, "x") {
			t.Errorf("FAIL: 50-gui.sh should be executable: %s", out)
		}
		t.Logf("PASS: %s", out)
	})

	t.Run("xvfb_running", func(t *testing.T) {
		_, code := exec(t, c, []string{"pgrep", "Xvfb"})
		if code != 0 {
			t.Fatalf("FAIL: Xvfb process not found (exit %d)", code)
		}
		t.Logf("PASS: Xvfb is running")
	})

	t.Run("xrdp_running", func(t *testing.T) {
		_, code := exec(t, c, []string{"pgrep", "xrdp"})
		if code != 0 {
			t.Fatalf("FAIL: xrdp process not found (exit %d)", code)
		}
		t.Logf("PASS: xrdp is running")
	})

	t.Run("xrdp_listening", func(t *testing.T) {
		out, code := exec(t, c, []string{"sh", "-c",
			"grep -i 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep ' 0A '"})
		if code != 0 || !strings.Contains(strings.ToUpper(out), "0D3D") {
			t.Fatalf("FAIL: port 3389 (0x0D3D) not in LISTEN state:\n%s", out)
		}
		t.Logf("PASS: xrdp listening on :3389\n%s", out)
	})

	t.Run("rdp_connect_with_creds", func(t *testing.T) {
		// Find xfreerdp binary (freerdp package provides xfreerdp or xfreerdp3)
		_, code := exec(t, c, []string{"sh", "-c", "command -v xfreerdp"})
		if code != 0 {
			t.Skip("skipping: xfreerdp not on PATH")
		}
		// +auth-only: perform RDP authentication and disconnect (no display needed).
		// /sec:rdp: use RDP security (not NLA) matching xrdp default config.
		// /cert:ignore: accept self-signed cert generated by entrypoint.
		out, code := exec(t, c, []string{"sh", "-c",
			"xfreerdp +auth-only /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /sec:rdp /cert:ignore 2>&1"})
		if code != 0 {
			t.Fatalf("FAIL: xfreerdp +auth-only failed (exit %d):\n%s", code, out)
		}
		t.Logf("PASS: RDP auth-only connection succeeded\n%s", out)
	})

	t.Run("rdp_reject_wrong_password", func(t *testing.T) {
		_, code := exec(t, c, []string{"sh", "-c", "command -v xfreerdp"})
		if code != 0 {
			t.Skip("skipping: xfreerdp not on PATH")
		}
		// Wrong password should be rejected.
		_, code = exec(t, c, []string{"sh", "-c",
			"xfreerdp +auth-only /v:127.0.0.1:3389 /u:" + hostUser + " /p:wrongpass /sec:rdp /cert:ignore 2>&1"})
		if code == 0 {
			t.Errorf("FAIL: xfreerdp with wrong password should have failed but exit 0")
		} else {
			t.Logf("PASS: RDP correctly rejected wrong password (exit %d)", code)
		}
	})
}
