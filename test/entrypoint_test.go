package container_test

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// buildTestUserImage builds a user image from a scaffolded config directory.
// Returns the tag. Removes the image on cleanup.
func buildTestUserImage(t *testing.T, configDir string) string {
	t.Helper()
	tag := fmt.Sprintf("devcell-test-user:%s-%s", shortSHA(), time.Now().Format("20060102T150405"))
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
	baseImg := baseImage()

	// 2. Scaffold config dir with this base image.
	configDir := t.TempDir()
	t.Setenv("DEVCELL_BASE_IMAGE", baseImg)
	if err := scaffold.Scaffold(configDir, "", "", false); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Verify Dockerfile FROM line.
	dockerfile, err := os.ReadFile(filepath.Join(configDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.HasPrefix(string(dockerfile), "FROM "+baseImg) {
		t.Fatalf("Dockerfile FROM doesn't match base image: got %.80s", string(dockerfile))
	}
	t.Logf("Scaffold OK: Dockerfile FROM %s", baseImg)

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
}

// TestEntrypointDebugTimestamps verifies that DEVCELL_DEBUG=true produces
// timestamped log lines in the format [X.XXXs].
func TestEntrypointDebugTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := baseImage()

	out, err := osexec.Command("docker", "run", "--rm",
		"--user", "0",
		"-e", "HOST_USER=testuser",
		"-e", "APP_NAME=tstest",
		"-e", "DEVCELL_DEBUG=true",
		img,
		"echo", "ready",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	output := string(out)
	t.Logf("Debug output:\n%s", output)

	// Every log line should have a timestamp like [0.123s] or [1.456s]
	tsPattern := regexp.MustCompile(`\[\d+\.\d{3}s\]`)
	if !tsPattern.MatchString(output) {
		t.Fatalf("FAIL: no timestamped log lines found (expected [X.XXXs] format)")
	}

	// Verify multiple log lines have timestamps (not just one)
	matches := tsPattern.FindAllString(output, -1)
	t.Logf("PASS: found %d timestamped log lines", len(matches))
	if len(matches) < 2 {
		t.Errorf("expected at least 2 timestamped lines, got %d", len(matches))
	}

	// Verify no log lines WITHOUT timestamps (lines that look like log output but lack [X.XXXs])
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "ready" {
			continue
		}
		// Lines starting with ✓ or containing known log markers should have timestamps
		if (strings.Contains(line, "✓") || strings.Contains(line, "Installing") ||
			strings.Contains(line, "Starting") || strings.Contains(line, "Merging")) &&
			!tsPattern.MatchString(line) {
			t.Errorf("FAIL: log line missing timestamp: %s", line)
		}
	}
}

// TestEntrypointSilentWithoutDebug verifies that without DEVCELL_DEBUG, the
// entrypoint produces no log output (spinner is now in the Go CLI, not the entrypoint).
func TestEntrypointSilentWithoutDebug(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := baseImage()

	out, err := osexec.Command("docker", "run", "--rm",
		"--user", "0",
		"-e", "HOST_USER=testuser",
		"-e", "APP_NAME=myapp42",
		img,
		"echo", "ready",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	output := string(out)
	t.Logf("Non-debug output:\n%s", output)

	// No debug timestamps should appear
	tsPattern := regexp.MustCompile(`\[\d+\.\d{3}s\]`)
	if tsPattern.MatchString(output) {
		t.Errorf("FAIL: debug timestamps found in non-debug mode")
	} else {
		t.Logf("PASS: no debug timestamps in non-debug mode")
	}

	// No verbose log lines should appear
	for _, marker := range []string{"Installing global tool", "Starting Xvfb", "Starting fluxbox", "Merging Claude"} {
		if strings.Contains(output, marker) {
			t.Errorf("FAIL: debug log line leaked in non-debug mode: %s", marker)
		}
	}
	t.Logf("PASS: no debug log lines leaked")
}
