package container_test

import (
	"context"
	osexec "os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startRdpContainer starts a container with GUI enabled and publishes port 3389.
// Waits until xrdp is listening on 3389 (0x0D3D in /proc/net/tcp).
func startRdpContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        image(),
		ExposedPorts: []string{"3389/tcp", "5900/tcp"},
		Env: map[string]string{
			"HOST_USER":           hostUser,
			"APP_NAME":            "test",
			"DEVCELL_GUI_ENABLED": "true",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		// Port 3389 = 0x0D3D; LISTEN = 0A
		WaitingFor: wait.ForExec([]string{"sh", "-c",
			"grep -qi 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null && grep -qi ' 0A ' /proc/net/tcp6 /proc/net/tcp 2>/dev/null"}).
			WithStartupTimeout(90 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start RDP container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

func skipIfNoXrdp(t *testing.T, c testcontainers.Container) {
	t.Helper()
	_, code := exec(t, c, []string{"sh", "-c", "command -v xrdp"})
	if code != 0 {
		t.Skip("skipping: image lacks xrdp")
	}
}

// TestRdpXrdpOnPath — xrdp binary must be on PATH for the session user.
func TestRdpXrdpOnPath(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoXrdp(t, c)
	out, code := asUser(t, c, "command -v xrdp")
	if code != 0 {
		t.Fatalf("FAIL: xrdp not on PATH for session user (exit %d)", code)
	}
	t.Logf("PASS: %s", out)
}

// TestRdpListensOn3389 — with DEVCELL_GUI_ENABLED=true, xrdp must bind port 3389.
func TestRdpListensOn3389(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	_, code := exec(t, c, []string{"pgrep", "xrdp"})
	if code != 0 {
		t.Fatalf("FAIL: xrdp process not found (exit %d)", code)
	}
	out, code := exec(t, c, []string{"sh", "-c",
		"grep -i 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep ' 0A '"})
	if code != 0 || !strings.Contains(strings.ToUpper(out), "0D3D") {
		t.Errorf("FAIL: port 3389 (0x0D3D) not in LISTEN state:\n%s", out)
	} else {
		t.Logf("PASS: xrdp listening on :3389\n%s", out)
	}
}

// TestRdpPortPublishedToHost — the published 3389/tcp must map to a non-privileged host port.
func TestRdpPortPublishedToHost(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	ctx := context.Background()
	mapped, err := c.MappedPort(ctx, "3389/tcp")
	if err != nil {
		t.Fatalf("FAIL: no mapped port for 3389/tcp: %v", err)
	}
	port := mapped.Int()
	if port < 1024 || port > 65535 {
		t.Errorf("FAIL: mapped port %d outside unprivileged range", port)
	} else {
		t.Logf("PASS: 3389/tcp → host port %d", port)
	}
}

// TestRdpDockerPortByName — `docker port <name> 3389` must return the exact host port.
func TestRdpDockerPortByName(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	ctx := context.Background()

	name, err := c.Name(ctx)
	if err != nil {
		t.Fatalf("FAIL: could not get container name: %v", err)
	}
	name = strings.TrimPrefix(name, "/")

	out, err := osexec.Command("docker", "port", name, "3389").Output()
	if err != nil {
		t.Fatalf("FAIL: 'docker port %s 3389' failed: %v", name, err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	lastColon := strings.LastIndex(firstLine, ":")
	if lastColon < 0 {
		t.Fatalf("FAIL: unexpected docker port output: %q", firstLine)
	}
	hostPort := firstLine[lastColon+1:]

	mapped, err := c.MappedPort(ctx, "3389/tcp")
	if err != nil {
		t.Fatalf("FAIL: could not get MappedPort: %v", err)
	}
	want := mapped.Port()
	if hostPort != want {
		t.Errorf("FAIL: docker port → %q, want %q", hostPort, want)
	} else {
		t.Logf("PASS: docker port %s 3389 → %s", name, hostPort)
	}
}
