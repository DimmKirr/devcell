package container_test

import (
	"context"
	osexec "os/exec"
	"strconv"
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

// TestRdpConnectWithCreds — xfreerdp +auth-only with correct creds must succeed.
// Note: xfreerdp may return non-zero exit code even on successful auth due to
// TLS negotiation teardown issues. We check xfreerdp's own "exit status" output.
func TestRdpConnectWithCreds(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	out, _ := exec(t, c, []string{"sh", "-c",
		"DISPLAY=:99 xfreerdp +auth-only /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /sec:rdp /cert:ignore 2>&1"})
	// xfreerdp logs "Authentication only, exit status N" where 0 = success.
	// Note: process exit code may be non-zero (147/signal) due to TLS teardown even on success.
	if strings.Contains(out, "exit status 0") {
		t.Logf("PASS: RDP auth-only connection succeeded")
	} else if strings.Contains(out, "Authentication only") {
		t.Errorf("FAIL: xfreerdp auth returned non-zero status:\n%s", out)
	} else {
		t.Errorf("FAIL: unexpected xfreerdp output (no auth status line):\n%s", out)
	}
}

// TestRdpNoLoginPrompt — xrdp auto-connects to VNC without showing a login
// screen. With [Xorg] removed and hardcoded creds in [vnc-any], the xrdp log
// should NOT contain "login_wnd".
func TestRdpNoLoginPrompt(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	// Clear xrdp log, connect, then check log for VNC connection.
	exec(t, c, []string{"sh", "-c", "truncate -s0 /var/log/xrdp.log"})
	// Connect with correct creds — triggers xrdp to proxy to VNC.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /sec:rdp /cert:ignore /timeout:5000 2>&1 &" +
			" sleep 3 && kill %1 2>/dev/null; true"})
	// Check xrdp log: should contain VNC connection, not "login_wnd"
	out, _ := exec(t, c, []string{"sh", "-c", "cat /var/log/xrdp.log 2>/dev/null"})
	if strings.Contains(out, "login_wnd") {
		t.Errorf("FAIL: xrdp showed login window (login_wnd found in log):\n%s", out)
	}
	if strings.Contains(out, "VNC started connecting") ||
		strings.Contains(out, "lib_mod_connect") ||
		strings.Contains(out, "libvnc") {
		t.Logf("PASS: xrdp auto-connected to VNC (no login prompt)\n%s", out)
	} else {
		t.Logf("WARN: could not confirm VNC auto-connect from log (may need DEBUG level):\n%s", out)
	}
}

// TestRdpKickExistingConnection — x11vnc runs without -shared, so a new
// VNC connection (from a second RDP session) should disconnect the first.
func TestRdpKickExistingConnection(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	// Start first connection in background.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /sec:rdp /cert:ignore 2>/dev/null &"})
	time.Sleep(3 * time.Second)

	// Start second connection — should kick the first.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /sec:rdp /cert:ignore 2>/dev/null &"})
	time.Sleep(3 * time.Second)

	// After second connect, only one ESTABLISHED VNC connection should remain.
	out, _ := exec(t, c, []string{"sh", "-c",
		"grep '170C' /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep -c ' 01 '"})
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	if count <= 1 {
		t.Logf("PASS: only %d ESTABLISHED VNC connection(s) — old connection was kicked", count)
	} else {
		t.Errorf("FAIL: expected 1 ESTABLISHED VNC connection after kick, got %d", count)
	}

	// Clean up background xfreerdp processes.
	exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
}

// TestRdpDesktopScreenshot — capture X11 screenshot, verify it's non-trivial.
func TestRdpDesktopScreenshot(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	_, code := exec(t, c, []string{"sh", "-c", "command -v import"})
	if code != 0 {
		t.Skip("skipping: ImageMagick `import` not on PATH")
	}
	out, code := exec(t, c, []string{"sh", "-c",
		"DISPLAY=:99 import -window root /tmp/screenshot.png 2>&1"})
	if code != 0 {
		t.Fatalf("FAIL: screenshot capture failed (exit %d):\n%s", code, out)
	}
	sizeOut, code := exec(t, c, []string{"sh", "-c", "stat -c %s /tmp/screenshot.png"})
	if code != 0 {
		t.Fatalf("FAIL: could not stat screenshot: exit %d", code)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		t.Fatalf("FAIL: could not parse file size %q: %v", sizeOut, err)
	}
	if size < 10*1024 {
		t.Errorf("FAIL: screenshot too small (%d bytes) — desktop may not be rendering", size)
	} else {
		t.Logf("PASS: desktop screenshot captured (%d bytes)", size)
	}

	identOut, _ := exec(t, c, []string{"sh", "-c",
		"identify /tmp/screenshot.png 2>/dev/null | head -1"})
	if strings.Contains(identOut, "1920x1080") {
		t.Logf("PASS: screenshot resolution 1920x1080")
	} else {
		t.Logf("WARN: unexpected resolution: %s", identOut)
	}
}

// TestRdpLogsToFile — xrdp logs must go to /var/log, not stdout.
func TestRdpLogsToFile(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	out, code := exec(t, c, []string{"sh", "-c",
		"test -f /var/log/xrdp.log && test -f /var/log/xrdp-sesman.log && echo OK"})
	if code != 0 || !strings.Contains(out, "OK") {
		t.Fatalf("FAIL: xrdp log files not found in /var/log/")
	}
	out, _ = exec(t, c, []string{"sh", "-c",
		"grep SyslogLevel /tmp/xrdp/xrdp.ini /tmp/xrdp/sesman.ini 2>/dev/null"})
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("FAIL: SyslogLevel should be DISABLED:\n%s", out)
	} else {
		t.Logf("PASS: xrdp logs to /var/log/, SyslogLevel=DISABLED\n%s", out)
	}
}

// TestRdpNoXorgSection — [Xorg] section must be removed from xrdp.ini.
func TestRdpNoXorgSection(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	out, _ := exec(t, c, []string{"sh", "-c",
		"grep -c '\\[Xorg\\]' /tmp/xrdp/xrdp.ini 2>/dev/null || echo 0"})
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	if count > 0 {
		t.Errorf("FAIL: [Xorg] section should be removed from xrdp.ini (found %d)", count)
	} else {
		t.Logf("PASS: no [Xorg] section in xrdp.ini")
	}
}

// TestRdpUserPasswordSet — user must have a password for sesman PAM auth.
func TestRdpUserPasswordSet(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	out, code := exec(t, c, []string{"sh", "-c",
		"getent shadow " + hostUser + " | cut -d: -f2 | head -c3"})
	if code != 0 {
		t.Fatalf("FAIL: could not read shadow entry (exit %d)", code)
	}
	if strings.HasPrefix(out, "$") {
		t.Logf("PASS: user %s has a password set (hash prefix: %s...)", hostUser, out)
	} else {
		t.Errorf("FAIL: user %s password appears locked or empty: %q", hostUser, out)
	}
}

// skipIfNoXfreerdp skips the test if xfreerdp is not on PATH inside the container.
func skipIfNoXfreerdp(t *testing.T, c testcontainers.Container) {
	t.Helper()
	_, code := exec(t, c, []string{"sh", "-c", "command -v xfreerdp"})
	if code != 0 {
		t.Skip("skipping: xfreerdp not on PATH")
	}
}
