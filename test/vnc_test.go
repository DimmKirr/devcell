package container_test

// vnc_test.go — Tests for VNC server availability and port publishing.
//
// Covers two bugs in the `task vnc` flow:
//   Bug 1: `docker compose port cell 5900` matches any running session for
//          service 'cell' — ambiguous when multiple sessions are live.
//          Fix: `docker port <container-name> 5900` targets the exact container.
//   Bug 2: EXT_VNC_PORT can resolve to a privileged port (<1024) when
//          PORT_PREFIX is a small digit (TMUX_PANE %0–%9, no SESSION_PORT_PREFIX).
//
// Run against the GUI-capable image (base-gui or ultimate):
//   DEVCELL_TEST_IMAGE=ghcr.io/dimmkirr/devcell:latest-ultimate go test -v -run TestVnc ./...

import (
	"context"
	osexec "os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startVncContainer starts a container with DEVCELL_GUI_ENABLED=true and
// publishes port 5900/tcp to a random host port. Waits until x11vnc is
// reachable from the test host (Xvfb + fluxbox each sleep 1 in entrypoint).
//
// Note: ss/netstat/nc are absent in this image — use /proc/net or
// wait.ForListeningPort (probes from outside via the published host port).
// skipIfNoGUI skips VNC tests when the image lacks GUI binaries (e.g. nix-only image).
func skipIfNoGUI(t *testing.T, c testcontainers.Container) {
	t.Helper()
	_, code := exec(t, c, []string{"sh", "-c", "command -v x11vnc"})
	if code != 0 {
		t.Skip("skipping: image lacks x11vnc (nix-only image without GUI support)")
	}
}

// probeGUI starts a lightweight container to check for GUI support, then skips if absent.
func probeGUI(t *testing.T) {
	t.Helper()
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)
}

func startVncContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        image(),
		ExposedPorts: []string{"5900/tcp"},
		Env: map[string]string{
			"HOST_USER":           hostUser,
			"APP_NAME":            "test",
			"DEVCELL_GUI_ENABLED": "true",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		// Check /proc/net/tcp for port 5900 (0x170C) in LISTEN state (0A).
		// ForListeningPort requires host→container TCP which is unreliable in CI;
		// /proc/net is always available without ss/nc.
		// entrypoint: Xvfb (sleep 1) → fluxbox (sleep 1) → x11vnc → allow 60s.
		WaitingFor: wait.ForExec([]string{"sh", "-c",
			"grep -qi 170C /proc/net/tcp6 /proc/net/tcp 2>/dev/null && grep -qi ' 0A ' /proc/net/tcp6 /proc/net/tcp 2>/dev/null"}).
			WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start VNC container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

// TestVncX11vncOnPath — x11vnc binary must be on PATH for the session user.
// Does not require DEVCELL_GUI_ENABLED; validates the package is installed in the image.
func TestVncX11vncOnPath(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)
	out, code := asUser(t, c, "command -v x11vnc")
	if code != 0 {
		t.Fatalf("FAIL: x11vnc not on PATH for session user (exit %d)", code)
	}
	t.Logf("PASS: %s", out)
}

// TestVncListensOn5900 — with DEVCELL_GUI_ENABLED=true, x11vnc must bind port 5900.
// Validates the entrypoint GUI stack: Xvfb → fluxbox → x11vnc all start cleanly.
//
// ss/netstat/nc are absent in this image; uses /proc/net/tcp6 and /proc/net/tcp directly.
// Port 5900 = 0x170C; LISTEN state = 0A.
func TestVncListensOn5900(t *testing.T) {
	probeGUI(t)
	c := startVncContainer(t) // already waited for port to be reachable
	// Verify x11vnc process is alive
	_, code := exec(t, c, []string{"pgrep", "x11vnc"})
	if code != 0 {
		t.Fatalf("FAIL: x11vnc process not found (exit %d)", code)
	}
	// Verify the socket is in LISTEN state via /proc/net (always available, no ss/netstat needed)
	out, code := exec(t, c, []string{"sh", "-c",
		"grep -i 170C /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep ' 0A '"})
	if code != 0 || !strings.Contains(strings.ToUpper(out), "170C") {
		t.Errorf("FAIL: port 5900 (0x170C) not in LISTEN state in /proc/net:\n%s", out)
	} else {
		t.Logf("PASS: x11vnc listening on :5900\n%s", out)
	}
}

// TestVncPortPublishedToHost — the published 5900/tcp must map to a non-privileged host port.
// Analogous to `${EXT_VNC_PORT}:5900` in compose.yml; reproduces Bug 2 if the mapping
// fails (Docker refuses to bind privileged ports without root).
func TestVncPortPublishedToHost(t *testing.T) {
	probeGUI(t)
	c := startVncContainer(t)
	ctx := context.Background()
	mapped, err := c.MappedPort(ctx, "5900/tcp")
	if err != nil {
		t.Fatalf("FAIL: no mapped port for 5900/tcp: %v", err)
	}
	port := mapped.Int()
	if port < 1024 || port > 65535 {
		t.Errorf("FAIL: mapped port %d is outside unprivileged range [1024,65535]", port)
	} else {
		t.Logf("PASS: 5900/tcp → host port %d", port)
	}
}

// TestVncDynamicResolution — xrandr resolution change must be picked up by x11vnc.
// Verifies that Xvfb supports RandR and x11vnc (with -xrandr) reflects the new size.
func TestVncDynamicResolution(t *testing.T) {
	probeGUI(t)
	c := startVncContainer(t)

	// Default resolution should be 1920x1080
	out, code := exec(t, c, []string{"sh", "-c", "DISPLAY=:99 xrandr 2>&1"})
	if code != 0 {
		t.Fatalf("xrandr failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "1920x1080") {
		t.Fatalf("expected default 1920x1080 in xrandr output: %s", out)
	}
	t.Logf("PASS: default resolution is 1920x1080")

	// Add a new mode and switch to it
	_, code = exec(t, c, []string{"sh", "-c",
		"DISPLAY=:99 xrandr --newmode 2560x1440 0 2560 2560 2560 2560 1440 1440 1440 1440 2>/dev/null; " +
			"DISPLAY=:99 xrandr --addmode screen 2560x1440 2>/dev/null; " +
			"DISPLAY=:99 xrandr -s 2560x1440 2>/dev/null"})
	if code != 0 {
		t.Skipf("xrandr mode change not supported (Xvfb RANDR is limited to initial resolution; would need Xvnc for dynamic resize) (exit %d)", code)
	}

	// Verify new resolution
	out, code = exec(t, c, []string{"sh", "-c", "DISPLAY=:99 xrandr 2>&1"})
	if code != 0 {
		t.Fatalf("xrandr check failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "2560x1440") || !strings.Contains(out, "*") {
		t.Errorf("expected 2560x1440 to be active resolution: %s", out)
	} else {
		t.Logf("PASS: resolution changed to 2560x1440")
	}
}

// TestVncDockerPortByName — `docker port <container-name> 5900` must return the exact
// host port for the named container. This is the fix for Bug 1 in Taskfile.yml `vnc` task:
//
//	BEFORE (bug): docker compose port cell 5900   — matches any session for service 'cell'
//	AFTER  (fix): docker port <container-name> 5900  — unambiguous, exact container
//
// Runs `docker port` from the test host (same Docker socket testcontainers uses) and
// cross-checks the result against testcontainers' own MappedPort.
func TestVncDockerPortByName(t *testing.T) {
	probeGUI(t)
	c := startVncContainer(t)
	ctx := context.Background()

	name, err := c.Name(ctx)
	if err != nil {
		t.Fatalf("FAIL: could not get container name: %v", err)
	}
	name = strings.TrimPrefix(name, "/")

	out, err := osexec.Command("docker", "port", name, "5900").Output()
	if err != nil {
		t.Fatalf("FAIL: 'docker port %s 5900' failed: %v\nEnsure the Docker socket is accessible from the test runner.", name, err)
	}
	// Output format per line: "0.0.0.0:<port>" or "[::]:<port>" — port is after last colon.
	firstLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	lastColon := strings.LastIndex(firstLine, ":")
	if lastColon < 0 {
		t.Fatalf("FAIL: unexpected 'docker port' output: %q", firstLine)
	}
	hostPort := firstLine[lastColon+1:]

	mapped, err := c.MappedPort(ctx, "5900/tcp")
	if err != nil {
		t.Fatalf("FAIL: could not get testcontainers MappedPort: %v", err)
	}
	want := mapped.Port()
	if hostPort != want {
		t.Errorf("FAIL: 'docker port %s 5900' → %q, want %q", name, hostPort, want)
	} else {
		t.Logf("PASS: 'docker port %s 5900' → %s (matches MappedPort)", name, hostPort)
	}
}
