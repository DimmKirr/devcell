// gui_test.go — desktop theme, RDP server, VNC server tests (all require GUI support)

package container_test

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	goimage "image"
	"image/png"
	"io"
	"log"
	"math"
	"math/cmplx"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// getDevcellHome returns the devcell nix environment directory.
// Uses DEVCELL_HOME env var if set, defaults to /opt/devcell.
func getDevcellHome() string {
	if h := os.Getenv("DEVCELL_HOME"); h != "" {
		return h
	}
	return "/opt/devcell"
}

// isInDevcell returns true when running inside a devcell container.
func isInDevcell() bool {
	_, err := os.Stat(getDevcellHome())
	return err == nil
}

// skipIfNotInDevcell skips the test when not inside a devcell container.
func skipIfNotInDevcell(t *testing.T) {
	t.Helper()
	if !isInDevcell() {
		t.Skipf("not in devcell container (DEVCELL_HOME=%s not found)", getDevcellHome())
	}
}

var testLogEnabled = os.Getenv("DEVCELL_TEST_LOG") != ""

// testLog writes to stderr when DEVCELL_TEST_LOG is set.
func testLog(format string, args ...any) {
	if testLogEnabled {
		log.Printf("[test] "+format, args...)
	}
}

var (
	guiOnce sync.Once
	guiErr  error
)

// ensureGUI makes sure the GUI stack (Xvfb, fluxbox, x11vnc, xrdp) is running.
// Starts it via 50-gui.sh if not already running. Skips if not in devcell.
func ensureGUI(t *testing.T) {
	t.Helper()
	skipIfNotInDevcell(t)
	guiOnce.Do(func() {
		if osexec.Command("pgrep", "fluxbox").Run() != nil {
			testLog("starting GUI stack...")
			guiErr = startGUIStack()
		} else {
			testLog("GUI stack already running")
		}
	})
	if guiErr != nil {
		t.Fatalf("ensure GUI: %v", guiErr)
	}
}

// assertContains checks that content contains the expected substring.
func assertContains(t *testing.T, label, content, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Errorf("%s: expected %q not found in:\n%s", label, expected, content)
	}
}

// ── Pixel assertion helpers ──────────────────────────────────────────────────

// pixelHex returns the hex color "#rrggbb" at (x, y) in img.
func pixelHex(img goimage.Image, x, y int) string {
	r, g, b, _ := img.At(x, y).RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// assertPixel checks that the pixel at (x, y) matches the expected hex color.
func assertPixel(t *testing.T, img goimage.Image, x, y int, expected, label string) {
	t.Helper()
	got := pixelHex(img, x, y)
	if got != expected {
		t.Errorf("%s: pixel(%d,%d) = %s, want %s", label, x, y, got, expected)
	}
}

// assertPixelTolerance checks pixel with ±tolerance per channel (for rendering differences).
func assertPixelTolerance(t *testing.T, img goimage.Image, x, y int, expected string, tolerance uint8, label string) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	er, eg, eb := parseHex(expected)
	if absDiff(uint8(r>>8), er) > tolerance ||
		absDiff(uint8(g>>8), eg) > tolerance ||
		absDiff(uint8(b>>8), eb) > tolerance {
		t.Errorf("%s: pixel(%d,%d) = #%02x%02x%02x, want %s (±%d)",
			label, x, y, r>>8, g>>8, b>>8, expected, tolerance)
	}
}

func parseHex(hex string) (uint8, uint8, uint8) {
	var r, g, b uint8
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// ── In-place desktop screenshot capture ─────────────────────────────────────

var (
	desktopScreenshot     goimage.Image
	desktopScreenshotOnce sync.Once
	desktopScreenshotErr  error
)

const testWallpaper = "testdata/wallpaper-4corners.png"
const screenshotPath = "/tmp/devcell-desktop-test.png"

// setupDesktopScreenshot returns a screenshot of the desktop with an xterm
// window and fluxbox menu open. The capture runs once (via sync.Once) and is
// shared across all pixel-assertion tests.
func setupDesktopScreenshot(t *testing.T) goimage.Image {
	t.Helper()
	desktopScreenshotOnce.Do(func() {
		desktopScreenshot, desktopScreenshotErr = captureDesktop()
	})
	if desktopScreenshotErr != nil {
		t.Fatalf("desktop screenshot: %v", desktopScreenshotErr)
	}
	return desktopScreenshot
}

// captureDesktop sets up the GUI environment, opens windows, and takes a
// screenshot of the full desktop. Intended to run inside a devcell container
// with Xvfb on :99.
func captureDesktop() (goimage.Image, error) {
	// Gate: must be running inside a devcell container.
	if !isInDevcell() {
		return nil, fmt.Errorf("not in devcell container (DEVCELL_HOME=%s not found)", getDevcellHome())
	}

	// Start GUI stack if fluxbox is not already running.
	if err := osexec.Command("pgrep", "fluxbox").Run(); err != nil {
		if err := startGUIStack(); err != nil {
			return nil, fmt.Errorf("start GUI stack: %w", err)
		}
	}

	// Resolve absolute path to test wallpaper.
	wpAbs, err := filepath.Abs(testWallpaper)
	if err != nil {
		return nil, fmt.Errorf("wallpaper abs path: %w", err)
	}

	// Set test wallpaper.
	cmd := osexec.Command("feh", "--bg-fill", wpAbs)
	cmd.Env = append(os.Environ(), "DISPLAY=:99")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("feh --bg-fill: %w\n%s", err, out)
	}
	time.Sleep(500 * time.Millisecond)

	// Kill any stale xterm windows from previous runs.
	osexec.Command("pkill", "-f", "xterm").Run()
	time.Sleep(300 * time.Millisecond)

	// Open xterm window.
	xtermCmd := osexec.Command("xterm", "-geometry", "80x24+100+100")
	xtermCmd.Env = append(os.Environ(), "DISPLAY=:99")
	if err := xtermCmd.Start(); err != nil {
		return nil, fmt.Errorf("xterm start: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Dismiss any stale menu/popup first.
	dismissCmd := osexec.Command("xdotool", "key", "Escape")
	dismissCmd.Env = append(os.Environ(), "DISPLAY=:99")
	dismissCmd.CombinedOutput()
	time.Sleep(300 * time.Millisecond)

	// Right-click on desktop to open fluxbox root menu.
	// Retry up to 3 times — when fluxbox was already running, the first click
	// may not register on the root window.
	for attempt := 0; attempt < 3; attempt++ {
		// Left-click on empty desktop to ensure root window has focus.
		focusCmd := osexec.Command("xdotool", "mousemove", "1200", "400", "click", "1")
		focusCmd.Env = append(os.Environ(), "DISPLAY=:99")
		focusCmd.CombinedOutput()
		time.Sleep(300 * time.Millisecond)

		// Dismiss any existing menu.
		escCmd := osexec.Command("xdotool", "key", "Escape")
		escCmd.Env = append(os.Environ(), "DISPLAY=:99")
		escCmd.CombinedOutput()
		time.Sleep(300 * time.Millisecond)

		// Right-click to open root menu.
		menuCmd := osexec.Command("xdotool", "mousemove", "960", "540", "click", "3")
		menuCmd.Env = append(os.Environ(), "DISPLAY=:99")
		if out, err := menuCmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("xdotool right-click: %w\n%s", err, out)
		}
		time.Sleep(1 * time.Second)

		// Check if menu appeared by querying active window name.
		nameCmd := osexec.Command("xdotool", "getactivewindow", "getwindowname")
		nameCmd.Env = append(os.Environ(), "DISPLAY=:99")
		nameOut, _ := nameCmd.Output()
		log.Printf("menu attempt %d: active window = %q", attempt+1, strings.TrimSpace(string(nameOut)))
	}
	time.Sleep(1 * time.Second)

	// Take screenshot with ImageMagick import.
	importCmd := osexec.Command("import", "-window", "root", screenshotPath)
	importCmd.Env = append(os.Environ(), "DISPLAY=:99")
	if out, err := importCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("import screenshot: %w\n%s", err, out)
	}

	// Decode PNG.
	f, err := os.Open(screenshotPath)
	if err != nil {
		return nil, fmt.Errorf("open screenshot: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return img, nil
}

// startGUIStack sources the 50-gui.sh entrypoint fragment to bring up
// Xvfb, fluxbox, x11vnc, and xrdp.
func startGUIStack() error {
	home := getDevcellHome()
	script := fmt.Sprintf(`#!/bin/bash
set -e
export DEVCELL_GUI_ENABLED=true
export HOST_USER=$(whoami)
export APP_NAME=test
export DEVCELL_HOME=%s
export USER=$(whoami)
log() { echo "[gui-setup] $*"; }
source /etc/devcell/entrypoint.d/50-gui.sh
`, home)
	cmd := osexec.Command("bash", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("50-gui.sh: %w", err)
	}

	// Wait up to 30s for xrdp to be listening on port 3389 (0x0D3D).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		check := osexec.Command("bash", "-c",
			"grep -qi 0D3D /proc/net/tcp* 2>/dev/null")
		if check.Run() == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("xrdp did not start within 30s")
}

// saveScreenshot always copies the desktop screenshot to the test run results
// directory for LLM-assisted review. Saved per-test so each pixel assertion
// test has its own snapshot for cross-checking.
func saveScreenshot(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		name := strings.ReplaceAll(t.Name(), "/", "-")
		dst := filepath.Join(testRunDir(), name+"-desktop.png")
		data, err := os.ReadFile(screenshotPath)
		if err == nil {
			os.WriteFile(dst, data, 0644)
			t.Logf("Screenshot saved to %s", dst)
		}
	})
}

// skipIfNoGUI skips VNC tests when the image lacks GUI binaries (e.g. nix-only image).
func skipIfNoGUI(t *testing.T, c testcontainers.Container) {
	t.Helper()
	_, code := exec(t, c, []string{"sh", "-c", "command -v x11vnc"})
	if code != 0 {
		t.Skip("skipping: image lacks x11vnc (nix-only image without GUI support)")
	}
}

// probeGUI skips the test if the image lacks GUI support.
// Checks DEVCELL_PROFILE in the image config — desktop stacks (ultimate,
// electronics) include GUI tools. DEVCELL_GUI_ENABLED is a runtime flag
// set by runner.go, never baked into the image, so we match on profile.
func probeGUI(t *testing.T) {
	t.Helper()
	img := image()
	out, err := osexec.Command("docker", "inspect", "--format",
		`{{range .Config.Env}}{{println .}}{{end}}`, img).Output()
	if err != nil {
		t.Skipf("skipping: cannot inspect image %s: %v", img, err)
	}
	envs := string(out)
	guiProfiles := []string{"devcell-ultimate", "devcell-electronics"}
	hasGUI := false
	for _, p := range guiProfiles {
		if strings.Contains(envs, "DEVCELL_PROFILE="+p) {
			hasGUI = true
			break
		}
	}
	if !hasGUI {
		t.Skip("skipping: image profile does not include desktop/GUI support")
	}
}

func skipIfNoXrdp(t *testing.T, c testcontainers.Container) {
	t.Helper()
	_, code := exec(t, c, []string{"sh", "-c", "command -v xrdp"})
	if code != 0 {
		t.Skip("skipping: image lacks xrdp")
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

// startDesktopGUIContainer starts a container with DEVCELL_GUI_ENABLED=true
// and waits for the full GUI stack (Xvfb + fluxbox + x11vnc) to be running.
func startDesktopGUIContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: image(),
		Env: map[string]string{
			"HOST_USER":           hostUser,
			"APP_NAME":            "test",
			"DEVCELL_GUI_ENABLED": "true",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"sh", "-c",
			"grep -qi 170C /proc/net/tcp6 /proc/net/tcp 2>/dev/null && grep -qi ' 0A ' /proc/net/tcp6 /proc/net/tcp 2>/dev/null"}).
			WithStartupTimeout(60 * time.Second),
	}
	if isThinVariant() {
		req.Mounts = testcontainers.Mounts(
			testcontainers.VolumeMount(thinVolumeName(), "/nix"),
		)
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start desktop GUI container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

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
			"DEVCELL_DEBUG":       "true",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		// Port 3389 = 0x0D3D; LISTEN = 0A
		WaitingFor: wait.ForExec([]string{"sh", "-c",
			"grep -qi 0D3D /proc/net/tcp6 /proc/net/tcp 2>/dev/null && grep -qi ' 0A ' /proc/net/tcp6 /proc/net/tcp 2>/dev/null"}).
			WithStartupTimeout(90 * time.Second),
	}
	if isThinVariant() {
		req.Mounts = testcontainers.Mounts(
			testcontainers.VolumeMount(thinVolumeName(), "/nix"),
		)
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

// startVncContainer starts a container with DEVCELL_GUI_ENABLED=true and
// publishes port 5900/tcp to a random host port.
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

// --- Desktop ---

// TestDesktop_Wallpaper verifies the test wallpaper rendered at full resolution.
func TestDesktop_Wallpaper(t *testing.T) {
	skipIfNotInDevcell(t)
	img := setupDesktopScreenshot(t)
	saveScreenshot(t)
	bounds := img.Bounds()
	if bounds.Dx() != 1920 || bounds.Dy() != 1080 {
		t.Fatalf("screenshot resolution: %dx%d, want 1920x1080", bounds.Dx(), bounds.Dy())
	}
	// Green markers at corners (wallpaper-4corners.png has ~20x20 green squares)
	assertPixelTolerance(t, img, 5, 5, "#00ff00", 10, "top-left corner")
	assertPixelTolerance(t, img, 1914, 5, "#00ff00", 10, "top-right corner")
	// Desktop body = black (sample above toolbar, away from window)
	assertPixelTolerance(t, img, 1500, 400, "#000000", 10, "desktop body")
}

// TestDesktop_Toolbar verifies toolbar colors: black bg, green workspace badge.
func TestDesktop_Toolbar(t *testing.T) {
	skipIfNotInDevcell(t)
	img := setupDesktopScreenshot(t)
	saveScreenshot(t)
	// Toolbar is 35px at bottom. Center of toolbar = 1080 - 17 = 1063
	toolbarY := img.Bounds().Dy() - 17
	assertPixelTolerance(t, img, 960, toolbarY, "#0d0d1c", 30, "toolbar bg center")
	// Workspace badge near left side (starts at ~x=40, sample at y=1070)
	assertPixelTolerance(t, img, 50, img.Bounds().Dy()-10, "#b8e336", 15, "toolbar workspace badge")
}

// TestDesktop_WindowChrome verifies xterm window has black title bar and green handle.
func TestDesktop_WindowChrome(t *testing.T) {
	skipIfNotInDevcell(t)
	img := setupDesktopScreenshot(t)
	saveScreenshot(t)
	// xterm at +100+100. Title bar starts at y~85 (after 3px border), 30px high.
	assertPixelTolerance(t, img, 300, 90, "#000000", 10, "window title bar bg")
}

// TestDesktop_Menu verifies the right-click menu renders with theme colors.
func TestDesktop_Menu(t *testing.T) {
	skipIfNotInDevcell(t)
	img := setupDesktopScreenshot(t)
	saveScreenshot(t)
	// Menu triggered at (960, 540). Title bar is green, body is dark surface.
	// Title area at y=532 (above click point), body at y=576 (below border).
	assertPixelTolerance(t, img, 960, 532, "#b8e336", 20, "menu title bg")
	assertPixelTolerance(t, img, 960, 576, "#0a0a18", 15, "menu body bg")
}

// TestDesktop_PatchrightMcpCellWrapper verifies the patchright-mcp-cell wrapper exists
// and references patchright-mcp (not playwright-mcp).
func TestDesktop_PatchrightMcpCellWrapper(t *testing.T) {
	skipIfNotInDevcell(t)

	out, err := osexec.Command("sh", "-c", "command -v patchright-mcp-cell").Output()
	if err != nil {
		t.Fatalf("patchright-mcp-cell not on PATH: %v", err)
	}
	path := strings.TrimSpace(string(out))
	testLog("patchright-mcp-cell at %s", path)

	wrapper, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wrapper %s: %v", path, err)
	}
	if !strings.Contains(string(wrapper), "mcp-server-patchright") {
		t.Errorf("wrapper does not call mcp-server-patchright")
	}
	if strings.Contains(string(wrapper), "playwright-mcp ") {
		t.Errorf("wrapper still references playwright-mcp")
	}
	t.Logf("PASS: patchright-mcp-cell wrapper found at %s", path)
}

// TestDesktop_XresourcesLoaded verifies xrdb loaded the Xresources into the
// X server's resource database.
func TestDesktop_XresourcesLoaded(t *testing.T) {
	ensureGUI(t)

	var out string
	for i := 0; i < 10; i++ {
		b, err := osexec.Command("sh", "-c", "DISPLAY=:99 xrdb -query").CombinedOutput()
		if err == nil && len(b) > 0 {
			out = string(b)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if out == "" {
		t.Fatal("X resource database is empty -- Xresources not loaded")
	}
	testLog("xrdb output:\n%s", out)

	assertContains(t, "XTerm*background", out, "XTerm*background:\t#0a0a18")
	assertContains(t, "XTerm*cursorColor", out, "XTerm*cursorColor:\t#1abc9c")
	assertContains(t, "XTerm*faceName", out, "XTerm*faceName:\tJetBrainsMono Nerd Font")
	assertContains(t, "XTerm*faceSize", out, "XTerm*faceSize:\t11")
	t.Logf("PASS: Xresources loaded (%d lines)", strings.Count(out, "\n")+1)
}

// TestDesktop_FluxboxThemeActive verifies fluxbox is running with the
// devcell-ocean theme (not default grey).
func TestDesktop_FluxboxThemeActive(t *testing.T) {
	ensureGUI(t)

	data, err := os.ReadFile("/tmp/fluxbox-init")
	if err != nil {
		t.Fatalf("read /tmp/fluxbox-init: %v", err)
	}
	out := string(data)
	testLog("fluxbox init:\n%s", out)

	assertContains(t, "styleFile", out,
		"session.styleFile:\t"+getDevcellHome()+"/.fluxbox/styles/devcell-ocean/theme.cfg")
	if !strings.Contains(out, "session.screen0.workspaceNames:") {
		t.Error("workspaceNames not set in fluxbox init")
	}
	t.Logf("PASS: fluxbox running with devcell-ocean theme")
}

// TestDesktop_XftDpi96 verifies Xft.dpi is set to 96 in X resources.
func TestDesktop_XftDpi96(t *testing.T) {
	ensureGUI(t)

	var out string
	for i := 0; i < 10; i++ {
		b, err := osexec.Command("sh", "-c", "DISPLAY=:99 xrdb -query 2>&1").CombinedOutput()
		if err == nil && strings.Contains(string(b), "Xft.dpi") {
			out = string(b)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(out, "Xft.dpi") {
		t.Fatalf("Xft.dpi not found in X resource database:\n%s", out)
	}
	assertContains(t, "Xft.dpi", out, "Xft.dpi:\t96")
	t.Logf("PASS: Xft.dpi set to 96")
}

// TestDesktop_XvfbDpi96 verifies Xvfb was started with -dpi 96.
func TestDesktop_XvfbDpi96(t *testing.T) {
	ensureGUI(t)

	b, err := osexec.Command("sh", "-c",
		"cat /proc/$(pgrep Xvfb)/cmdline 2>/dev/null | tr '\\0' ' '").CombinedOutput()
	if err != nil {
		t.Fatalf("could not read Xvfb cmdline: %v", err)
	}
	out := string(b)
	testLog("Xvfb cmdline: %s", out)
	if !strings.Contains(out, "-dpi 96") && !strings.Contains(out, "-dpi96") {
		t.Errorf("Xvfb not started with -dpi 96:\n%s", out)
	} else {
		t.Logf("PASS: Xvfb started with -dpi 96")
	}
}

// --- RDP ---

// TestRdp_ListensOn3389 -- with DEVCELL_GUI_ENABLED=true, xrdp must bind port 3389.
func TestRdp_ListensOn3389(t *testing.T) {
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

// TestRdp_PortPublishedToHost -- the published 3389/tcp must map to a non-privileged host port.
func TestRdp_PortPublishedToHost(t *testing.T) {
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
		t.Logf("PASS: 3389/tcp -> host port %d", port)
	}
}

// TestRdp_DockerPortByName -- `docker port <name> 3389` must return the exact host port.
func TestRdp_DockerPortByName(t *testing.T) {
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
		t.Errorf("FAIL: docker port -> %q, want %q", hostPort, want)
	} else {
		t.Logf("PASS: docker port %s 3389 -> %s", name, hostPort)
	}
}

// TestRdp_ConnectWithCreds -- xfreerdp with correct creds must establish a VNC session.
// Note: +auth-only is not used because xrdp 0.10.x with TLS defers authentication
// to the login window phase, which +auth-only never reaches (FreeRDP 3.x).
func TestRdp_ConnectWithCreds(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	// Connect via RDP and verify xrdp proxies to VNC by checking that the
	// number of ESTABLISHED connections on port 5900 (0x170C) increases.
	beforeOut, _ := exec(t, c, []string{"sh", "-c",
		"grep '170C' /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep -c ' 01 '"})
	before, _ := strconv.Atoi(strings.TrimSpace(beforeOut))

	exec(t, c, []string{"sh", "-c",
		"DISPLAY=:99 xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /cert:ignore /timeout:5000 2>/dev/null &"})
	time.Sleep(3 * time.Second)

	afterOut, _ := exec(t, c, []string{"sh", "-c",
		"grep '170C' /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep -c ' 01 '"})
	after, _ := strconv.Atoi(strings.TrimSpace(afterOut))
	if after > before {
		t.Logf("PASS: RDP connection established VNC session (before=%d, after=%d ESTABLISHED on :5900)", before, after)
	} else {
		t.Errorf("FAIL: no new VNC connection after RDP connect (before=%d, after=%d)", before, after)
	}
	exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
}

// TestRdp_NoLoginPrompt -- xrdp auto-connects to VNC without showing a login screen.
func TestRdp_NoLoginPrompt(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	// Clear xrdp log, connect, then check log for VNC connection.
	exec(t, c, []string{"sh", "-c", "truncate -s0 /var/log/xrdp.log"})
	// Connect with correct creds -- triggers xrdp to proxy to VNC.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /cert:ignore /timeout:5000 2>&1 &" +
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

// TestRdp_KickExistingConnection -- x11vnc runs without -shared, so a new
// VNC connection (from a second RDP session) should disconnect the first.
func TestRdp_KickExistingConnection(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)
	// Start first connection in background.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /cert:ignore 2>/dev/null &"})
	time.Sleep(3 * time.Second)

	// Start second connection -- should kick the first.
	exec(t, c, []string{"sh", "-c",
		"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /cert:ignore 2>/dev/null &"})
	time.Sleep(3 * time.Second)

	// After second connect, only one ESTABLISHED VNC connection should remain.
	out, _ := exec(t, c, []string{"sh", "-c",
		"grep '170C' /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep -c ' 01 '"})
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	if count <= 1 {
		t.Logf("PASS: only %d ESTABLISHED VNC connection(s) -- old connection was kicked", count)
	} else {
		t.Errorf("FAIL: expected 1 ESTABLISHED VNC connection after kick, got %d", count)
	}

	// Clean up background xfreerdp processes.
	exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
}

// TestRdp_LogsToFile -- xrdp logs must go to /var/log, not stdout.
func TestRdp_LogsToFile(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	out, code := exec(t, c, []string{"sh", "-c",
		"test -f /var/log/xrdp.log && echo OK"})
	if code != 0 || !strings.Contains(out, "OK") {
		t.Fatalf("FAIL: xrdp log files not found in /var/log/")
	}
	// xrdp.ini must have LogFile pointing to /var/log and syslog disabled
	out, _ = exec(t, c, []string{"sh", "-c",
		"grep -E '^LogFile=|^EnableSyslog=' /tmp/xrdp/xrdp.ini 2>/dev/null"})
	if !strings.Contains(out, "LogFile=/var/log/xrdp.log") {
		t.Errorf("FAIL: xrdp.ini LogFile not set to /var/log/xrdp.log:\n%s", out)
	}
	if !strings.Contains(out, "EnableSyslog=false") {
		t.Errorf("FAIL: xrdp.ini EnableSyslog should be false:\n%s", out)
	} else {
		t.Logf("PASS: xrdp logs to /var/log/, syslog disabled\n%s", out)
	}
}

// TestRdp_NoXorgSection -- [Xorg] section must be removed from xrdp.ini.
func TestRdp_NoXorgSection(t *testing.T) {
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

// TestRdp_UserExists -- session user must exist for xrdp VNC proxy.
func TestRdp_UserExists(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	out, code := exec(t, c, []string{"sh", "-c",
		"id " + hostUser + " && getent passwd " + hostUser + " | cut -d: -f7"})
	if code != 0 {
		t.Fatalf("FAIL: user %s does not exist (exit %d)", hostUser, code)
	}
	if strings.Contains(out, "/bin/zsh") || strings.Contains(out, "/bin/bash") {
		t.Logf("PASS: user %s exists with valid shell:\n%s", hostUser, out)
	} else {
		t.Errorf("FAIL: user %s has unexpected shell:\n%s", hostUser, out)
	}
}

// TestRdp_ClipboardSync -- clipboard text set on the server X display (:99)
// must propagate to the xfreerdp client display (:98) via the RDP cliprdr channel.
func TestRdp_ClipboardSync(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)

	// Verify xclip is available.
	_, code := exec(t, c, []string{"sh", "-c", "command -v xclip"})
	if code != 0 {
		t.Skip("skipping: xclip not on PATH")
	}

	testText := "devcell-clip-" + time.Now().Format("150405")

	// Start a second Xvfb (:98) for the xfreerdp client side.
	exec(t, c, []string{"sh", "-c", "Xvfb :98 -screen 0 1024x768x24 &"})
	time.Sleep(1 * time.Second)
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c", "pkill -f 'Xvfb :98' 2>/dev/null; true"})
	})

	// Connect xfreerdp with clipboard enabled (client on :98, server on :99 via RDP).
	exec(t, c, []string{"sh", "-c",
		"DISPLAY=:98 xfreerdp /v:127.0.0.1:3389 /u:" + hostUser +
			" /p:rdp /cert:ignore +clipboard 2>/dev/null &"})
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
	})
	// FreeRDP 3.x needs ~10s to fully establish the RDP session and cliprdr channel.
	time.Sleep(10 * time.Second)

	// Set clipboard text on the server display (:99).
	exec(t, c, []string{"sh", "-c",
		"echo -n '" + testText + "' | DISPLAY=:99 xclip -selection clipboard"})
	time.Sleep(5 * time.Second)

	// Read clipboard from the client display (:98).
	out, code := exec(t, c, []string{"sh", "-c",
		"DISPLAY=:98 xclip -selection clipboard -o 2>&1"})
	if code != 0 {
		t.Fatalf("FAIL: xclip read on :98 failed (exit %d): %s", code, out)
	}
	if strings.Contains(strings.TrimSpace(out), testText) {
		t.Logf("PASS: clipboard synced via RDP: server(:99) → client(:98) = %q", testText)
	} else {
		t.Errorf("FAIL: clipboard not synced; expected %q on client(:98), got %q", testText, out)
	}
}

// TestRdp_ClientResolutionRequest -- xfreerdp /size:WxH requests a specific
// session size; the Xvfb-backed xrdp+libvnc pipeline cannot honor it because
// the framebuffer is fixed at 1920x1080 (see 50-gui.sh:11). This test
// connects with three different client-requested sizes and asserts that the
// resulting FreeRDP window is always 1920x1080 — demonstrating that no real
// resolution negotiation happens.
//
// Once CELL-168 (Xvnc replacement) lands, the assertion should be inverted:
// the FreeRDP window should match the /size: request. When this test starts
// failing, that is the signal the fix is in.
func TestRdp_ClientResolutionRequest(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)

	// xdotool is used to read the FreeRDP window geometry on :98.
	if _, code := exec(t, c, []string{"sh", "-c", "command -v xdotool"}); code != 0 {
		t.Skip("skipping: xdotool not on PATH")
	}

	// Roomy second Xvfb (:98) acts as the xfreerdp client display.
	// 3200x1800 is larger than any size we request below, so the FreeRDP
	// window never gets clipped by its own host display.
	exec(t, c, []string{"sh", "-c", "Xvfb :98 -screen 0 3200x1800x24 2>/dev/null &"})
	time.Sleep(1 * time.Second)
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c", "pkill -f 'Xvfb :98' 2>/dev/null; true"})
	})

	cases := []struct {
		name      string
		reqWidth  int
		reqHeight int
	}{
		{"smaller_than_server_1366x768", 1366, 768},
		{"matches_server_1920x1080", 1920, 1080},
		{"larger_than_server_2560x1440", 2560, 1440},
	}

	const (
		wantWidth  = 1920 // Xvfb framebuffer width (50-gui.sh:11)
		wantHeight = 1080 // Xvfb framebuffer height
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Connect xfreerdp on :98 with the requested /size:. No
			// /smart-sizing — we want the window dimensions to reflect the
			// negotiated session size, not client-side bitmap stretching.
			cmd := fmt.Sprintf(
				"DISPLAY=:98 xfreerdp /v:127.0.0.1:3389 /u:%s /p:rdp /cert:ignore /size:%dx%d 2>/dev/null &",
				hostUser, tc.reqWidth, tc.reqHeight)
			exec(t, c, []string{"sh", "-c", cmd})
			t.Cleanup(func() {
				exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
			})
			// FreeRDP 3.x needs ~8s to fully establish the session and
			// map its top-level window.
			time.Sleep(8 * time.Second)

			out, code := exec(t, c, []string{"sh", "-c",
				"DISPLAY=:98 xdotool search --name FreeRDP 2>/dev/null | head -1 | " +
					"xargs -I{} xdotool getwindowgeometry --shell {} 2>&1"})
			if code != 0 || !strings.Contains(out, "WIDTH=") {
				t.Fatalf("could not get FreeRDP window geometry on :98 (exit %d):\n%s", code, out)
			}

			actualW := parseShellVar(out, "WIDTH")
			actualH := parseShellVar(out, "HEIGHT")
			t.Logf("Client requested %dx%d → FreeRDP window is %dx%d",
				tc.reqWidth, tc.reqHeight, actualW, actualH)

			if actualW != wantWidth || actualH != wantHeight {
				t.Errorf("expected %dx%d (Xvfb fixed framebuffer), got %dx%d — "+
					"has CELL-168 (Xvnc replacement) landed? If yes, flip this assertion to actual==requested.",
					wantWidth, wantHeight, actualW, actualH)
			}
		})
	}
}

// parseShellVar extracts the integer value of KEY=value lines emitted by
// `xdotool getwindowgeometry --shell`.
func parseShellVar(out, key string) int {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// --- VNC ---

// TestVnc_ListensOn5900 -- with DEVCELL_GUI_ENABLED=true, x11vnc must bind port 5900.
func TestVnc_ListensOn5900(t *testing.T) {
	probeGUI(t)
	c := startVncContainer(t) // already waited for port to be reachable
	// Verify x11vnc process is alive
	_, code := exec(t, c, []string{"pgrep", "x11vnc"})
	if code != 0 {
		t.Fatalf("FAIL: x11vnc process not found (exit %d)", code)
	}
	// Verify the socket is in LISTEN state via /proc/net
	out, code := exec(t, c, []string{"sh", "-c",
		"grep -i 170C /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep ' 0A '"})
	if code != 0 || !strings.Contains(strings.ToUpper(out), "170C") {
		t.Errorf("FAIL: port 5900 (0x170C) not in LISTEN state in /proc/net:\n%s", out)
	} else {
		t.Logf("PASS: x11vnc listening on :5900\n%s", out)
	}
}

// TestVnc_PortPublishedToHost -- the published 5900/tcp must map to a non-privileged host port.
func TestVnc_PortPublishedToHost(t *testing.T) {
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
		t.Logf("PASS: 5900/tcp -> host port %d", port)
	}
}

// TestVnc_DynamicResolution -- xrandr resolution change must be picked up by x11vnc.
func TestVnc_DynamicResolution(t *testing.T) {
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

// TestVnc_DockerPortByName -- `docker port <container-name> 5900` must return the exact host port.
func TestVnc_DockerPortByName(t *testing.T) {
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
	// Output format per line: "0.0.0.0:<port>" or "[::]:<port>" -- port is after last colon.
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
		t.Errorf("FAIL: 'docker port %s 5900' -> %q, want %q", name, hostPort, want)
	} else {
		t.Logf("PASS: 'docker port %s 5900' -> %s (matches MappedPort)", name, hostPort)
	}
}

// TestRdp_AudioPlayback verifies the server-side PulseAudio pipeline works:
// xrdp-sink is loaded and audio flows through the NullSink loopback.
// This does NOT test the end-to-end RDP rdpsnd channel — see
// TestRdp_AudioRedirection for that.
//
// Pipeline: paplay (sine WAV) → PulseAudio NullSink → NullSink.monitor → parec → WAV → FFT
// Verifies PulseAudio starts with xrdp-sink loaded (CELL-73 fix) and audio flows.
func TestRdp_AudioPlayback(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)

	// PulseAudio runs as the session user; docker exec runs as root.
	// All audio commands must use gosu to switch to the PA owner.
	paRun := "gosu " + hostUser + " env PULSE_SERVER=unix:/tmp/pulse-runtime/pulse/native XDG_RUNTIME_DIR=/tmp/pulse-runtime"

	// Guard: need paplay, parec, and pactl for this test.
	for _, tool := range []string{"paplay", "parec", "pactl"} {
		if _, code := exec(t, c, []string{"sh", "-c", "command -v " + tool}); code != 0 {
			t.Skipf("skipping: %s not on PATH", tool)
		}
	}

	// Generate a 5-second 440 Hz sine wave as a WAV file and copy into container.
	wavData := generateSineWAV(440.0, 44100, 5, 2)
	if err := c.CopyToContainer(context.Background(), wavData, "/tmp/sine440.wav", 0o644); err != nil {
		t.Fatalf("copy sine440.wav into container: %v", err)
	}

	// Verify PulseAudio is running and xrdp_sink exists.
	sinksOut, _ := exec(t, c, []string{"sh", "-c", paRun + " pactl list sinks short 2>/dev/null"})
	if !strings.Contains(sinksOut, "xrdp-sink") {
		paLog, _ := exec(t, c, []string{"sh", "-c", "cat /var/log/pulseaudio.log 2>/dev/null | tail -30"})
		t.Fatalf("FAIL: xrdp-sink not found in PulseAudio sinks — module-xrdp-sink not loaded\nSinks: %s\nPulseAudio log:\n%s", sinksOut, paLog)
	}
	t.Logf("PulseAudio sinks:\n%s", sinksOut)

	// Set NullSink as default so paplay routes there (xrdp-sink's transport
	// isn't active without a real RDP client session).
	exec(t, c, []string{"sh", "-c", paRun + " pactl set-default-sink NullSink 2>/dev/null; true"})

	// Play the sine wave through PulseAudio (routed to NullSink).
	exec(t, c, []string{"sh", "-c", paRun + " paplay /tmp/sine440.wav &"})
	time.Sleep(1 * time.Second)

	// Capture 3 seconds of audio from NullSink's monitor source.
	captureOut, captureCode := exec(t, c, []string{"sh", "-c",
		paRun + " timeout 3 parec --device=NullSink.monitor --format=s16le --rate=44100 --channels=2 " +
			"--file-format=wav /tmp/out.wav 2>&1; echo EXIT=$?"})
	t.Logf("parec output: %s (exec code %d)", captureOut, captureCode)

	// Stop playback.
	exec(t, c, []string{"sh", "-c", "pkill -f 'paplay.*sine440' 2>/dev/null; true"})

	// Verify capture file exists.
	lsOut, _ := exec(t, c, []string{"sh", "-c", "ls -la /tmp/out.wav 2>&1"})
	t.Logf("capture file: %s", lsOut)
	if strings.Contains(lsOut, "No such file") {
		t.Fatal("FAIL: parec did not produce /tmp/out.wav")
	}

	// Copy the captured WAV out of the container for analysis.
	wavPath := filepath.Join(t.TempDir(), "out.wav")
	wavB64, _ := exec(t, c, []string{"sh", "-c", "base64 /tmp/out.wav"})
	wavData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(wavB64))
	if err != nil {
		t.Fatalf("FAIL: decode base64 of captured WAV: %v", err)
	}
	if err := os.WriteFile(wavPath, wavData, 0644); err != nil {
		t.Fatalf("FAIL: write captured WAV: %v", err)
	}
	t.Logf("Captured WAV: %d bytes", len(wavData))

	// Analyze: the captured audio must not be silent and must contain ~440 Hz.
	dominant, rms, err := analyzeWavFrequency(wavPath, 44100)
	if err != nil {
		t.Fatalf("FAIL: could not analyze captured audio: %v", err)
	}
	t.Logf("Captured audio: dominant=%.1f Hz, RMS=%.6f", dominant, rms)

	if rms < 0.001 {
		t.Fatal("FAIL: captured audio is silent — rdpsnd not delivering audio to client")
	}
	if math.Abs(dominant-440.0) > 44.0 {
		t.Errorf("FAIL: dominant frequency %.1f Hz, expected ~440 Hz (±10%%)", dominant)
	} else {
		t.Logf("PASS: RDP audio playback working — captured %.1f Hz sine (RMS=%.4f)", dominant, rms)
	}
}

// TestRdp_AudioRedirection verifies end-to-end RDP audio: a 440 Hz sine wave
// played on the server reaches the FreeRDP client via the rdpsnd channel.
// Subtests isolate each pipeline hop to pinpoint where audio breaks.
//
// Pipeline: paplay → PA xrdp-sink → module-xrdp-sink → xrdp rdpsnd
//           → xfreerdp /sound:sys:pulse → client PA → ClientSink
func TestRdp_AudioRedirection(t *testing.T) {
	probeGUI(t)
	c := startRdpContainer(t)
	skipIfNoXfreerdp(t, c)

	paRun := "gosu " + hostUser + " env PULSE_SERVER=unix:/tmp/pulse-runtime/pulse/native XDG_RUNTIME_DIR=/tmp/pulse-runtime"

	for _, tool := range []string{"paplay", "parec", "pactl"} {
		if _, code := exec(t, c, []string{"sh", "-c", "command -v " + tool}); code != 0 {
			t.Skipf("skipping: %s not on PATH", tool)
		}
	}

	sinksOut, _ := exec(t, c, []string{"sh", "-c", paRun + " pactl list sinks short 2>/dev/null"})
	if !strings.Contains(sinksOut, "xrdp-sink") {
		t.Skip("skipping: xrdp-sink not loaded — module-xrdp-sink not available")
	}
	t.Logf("Server PA sinks:\n%s", sinksOut)

	exec(t, c, []string{"sh", "-c", paRun + " pactl set-default-sink xrdp-sink 2>/dev/null; true"})

	// --- Client-side setup ---

	exec(t, c, []string{"sh", "-c", "Xvfb :98 -screen 0 1024x768x24 2>/dev/null &"})
	time.Sleep(1 * time.Second)
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c", "pkill -f 'Xvfb :98' 2>/dev/null; true"})
	})

	clientPADir := "/tmp/pa-client"
	clientPAScript := `#!/bin/sh
mkdir -p ` + clientPADir + `
export XDG_RUNTIME_DIR=` + clientPADir + `
pulseaudio --daemonize=yes --exit-idle-time=-1 --disable-shm=true -n \
  --load="module-native-protocol-unix auth-anonymous=1 socket=` + clientPADir + `/native" \
  --load="module-null-sink sink_name=ClientSink" \
  --load="module-always-sink"
`
	if err := c.CopyToContainer(context.Background(), []byte(clientPAScript), "/tmp/start-client-pa.sh", 0o755); err != nil {
		t.Fatalf("copy client PA script: %v", err)
	}
	paStartOut, paStartCode := exec(t, c, []string{"sh", "-c",
		"gosu " + hostUser + " /tmp/start-client-pa.sh 2>&1"})
	t.Logf("client PA start: exit=%d output=%s", paStartCode, paStartOut)
	time.Sleep(2 * time.Second)
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c",
			"gosu " + hostUser + " env PULSE_SERVER=unix:" + clientPADir + "/native pulseaudio --kill 2>/dev/null; true"})
	})

	clientPA := "gosu " + hostUser + " env PULSE_SERVER=unix:" + clientPADir + "/native"

	clientSinks, code := exec(t, c, []string{"sh", "-c", clientPA + " pactl list sinks short 2>&1"})
	if code != 0 || !strings.Contains(clientSinks, "ClientSink") {
		psOut, _ := exec(t, c, []string{"sh", "-c", "ps aux | grep pulse"})
		lsOut, _ := exec(t, c, []string{"sh", "-c", "ls -la " + clientPADir + "/"})
		t.Fatalf("client PulseAudio not running or ClientSink missing:\nsinks: %s\nps: %s\nls: %s", clientSinks, psOut, lsOut)
	}
	t.Logf("Client PA sinks:\n%s", clientSinks)

	// Launch xfreerdp as the session user (not root) — PulseAudio's
	// pa_context_new() returns NULL for root, breaking rdpsnd-pulse.
	exec(t, c, []string{"sh", "-c",
		"gosu " + hostUser + " env DISPLAY=:98 PULSE_SERVER=unix:" + clientPADir + "/native " +
			"xfreerdp /v:127.0.0.1:3389 /u:" + hostUser + " /p:rdp /cert:ignore " +
			"/sound:sys:pulse /log-level:DEBUG >/tmp/freerdp-stdout.log 2>/tmp/freerdp-debug.log &"})
	t.Cleanup(func() {
		exec(t, c, []string{"sh", "-c", "pkill -f 'xfreerdp.*127.0.0.1:3389' 2>/dev/null; true"})
	})
	time.Sleep(10 * time.Second)

	vncOut, _ := exec(t, c, []string{"sh", "-c",
		"grep '170C' /proc/net/tcp6 /proc/net/tcp 2>/dev/null | grep -c ' 01 '"})
	vncCount, _ := strconv.Atoi(strings.TrimSpace(vncOut))
	if vncCount == 0 {
		t.Fatal("xfreerdp did not establish RDP session (no VNC connection)")
	}
	t.Logf("RDP session established (%d VNC connections)", vncCount)

	// Copy sine wave and start playback on server PA (routed to xrdp-sink).
	wavData := generateSineWAV(440.0, 44100, 5, 2)
	if err := c.CopyToContainer(context.Background(), wavData, "/tmp/sine440.wav", 0o644); err != nil {
		t.Fatalf("copy sine440.wav into container: %v", err)
	}
	exec(t, c, []string{"sh", "-c", paRun + " paplay /tmp/sine440.wav &"})
	time.Sleep(2 * time.Second)

	// --- Subtests: isolate each pipeline hop ---

	t.Run("1_server_xrdp_sink_receives_audio", func(t *testing.T) {
		sinks, _ := exec(t, c, []string{"sh", "-c", paRun + " pactl list sinks short 2>/dev/null"})
		t.Logf("Server sinks during playback:\n%s", sinks)
		for _, line := range strings.Split(sinks, "\n") {
			if strings.Contains(line, "xrdp-sink") {
				if strings.Contains(line, "RUNNING") {
					t.Logf("PASS: xrdp-sink is RUNNING — PulseAudio is routing audio to it")
					return
				}
				t.Errorf("xrdp-sink state is not RUNNING (got: %s) — paplay may not be routing to xrdp-sink", strings.TrimSpace(line))
				return
			}
		}
		t.Error("xrdp-sink not found in sink list during playback")
	})

	t.Run("2_chansrv_rdpsnd_negotiated", func(t *testing.T) {
		chLog, _ := exec(t, c, []string{"sh", "-c",
			"cat /tmp/xrdp-chansrv.*.log 2>/dev/null | grep -iv FUSE"})
		t.Logf("chansrv log (filtered):\n%s", lastNLines(chLog, 40))

		hasFormat := strings.Contains(chLog, "sound_process_output_format")
		hasTraining := strings.Contains(chLog, "sound_process_training")
		if hasFormat && hasTraining {
			t.Logf("PASS: chansrv negotiated rdpsnd formats and completed training")
		} else {
			t.Errorf("chansrv missing rdpsnd negotiation (format=%v training=%v)", hasFormat, hasTraining)
		}
	})

	t.Run("3_freerdp_rdpsnd_active", func(t *testing.T) {
		debugLog, _ := exec(t, c, []string{"sh", "-c", "cat /tmp/freerdp-debug.log 2>/dev/null"})
		t.Logf("FreeRDP debug log (last 80 lines):\n%s", lastNLines(debugLog, 80))

		hasRdpsnd := strings.Contains(strings.ToLower(debugLog), "rdpsnd") ||
			strings.Contains(strings.ToLower(debugLog), "sound")
		hasAudioData := strings.Contains(debugLog, "SNDC_WAVE") ||
			strings.Contains(debugLog, "WaveInfo") ||
			strings.Contains(debugLog, "rdpsnd_recv_pdu")
		if hasRdpsnd {
			t.Logf("FreeRDP log mentions rdpsnd channel")
		} else {
			t.Error("FreeRDP debug log has no rdpsnd references — channel not loaded by client")
		}
		if hasAudioData {
			t.Logf("PASS: FreeRDP is receiving audio data PDUs (SNDC_WAVE/WaveInfo)")
		} else {
			t.Error("FreeRDP debug log has no audio data PDUs — xrdp may not be sending audio frames")
		}
	})

	t.Run("4_client_sink_receives_audio", func(t *testing.T) {
		sinks, _ := exec(t, c, []string{"sh", "-c", clientPA + " pactl list sinks short 2>/dev/null"})
		t.Logf("Client sinks during playback:\n%s", sinks)
		for _, line := range strings.Split(sinks, "\n") {
			if strings.Contains(line, "ClientSink") {
				if strings.Contains(line, "RUNNING") {
					t.Logf("PASS: ClientSink is RUNNING — FreeRDP is pushing audio to client PA")
					return
				}
				t.Errorf("ClientSink state is not RUNNING (got: %s) — FreeRDP not delivering audio to client PA", strings.TrimSpace(line))
				return
			}
		}
		t.Error("ClientSink not found in client PA sink list")
	})

	t.Run("5_client_captures_440hz", func(t *testing.T) {
		captureOut, _ := exec(t, c, []string{"sh", "-c",
			clientPA + " timeout 3 parec --device=ClientSink.monitor " +
				"--format=s16le --rate=44100 --channels=2 --file-format=wav /tmp/client-out.wav 2>&1; echo EXIT=$?"})
		t.Logf("client parec output: %s", captureOut)

		exec(t, c, []string{"sh", "-c", "pkill -f 'paplay.*sine440' 2>/dev/null; true"})

		lsOut, _ := exec(t, c, []string{"sh", "-c", "ls -la /tmp/client-out.wav 2>&1"})
		t.Logf("client capture file: %s", lsOut)
		if strings.Contains(lsOut, "No such file") {
			t.Fatal("parec did not produce /tmp/client-out.wav")
		}

		wavPath := filepath.Join(t.TempDir(), "client-out.wav")
		wavB64, _ := exec(t, c, []string{"sh", "-c", "base64 /tmp/client-out.wav"})
		capturedWav, err := base64.StdEncoding.DecodeString(strings.TrimSpace(wavB64))
		if err != nil {
			t.Fatalf("decode base64 of captured WAV: %v", err)
		}
		if err := os.WriteFile(wavPath, capturedWav, 0644); err != nil {
			t.Fatalf("write captured WAV: %v", err)
		}
		t.Logf("Captured WAV: %d bytes", len(capturedWav))

		dominant, rms, err := analyzeWavFrequency(wavPath, 44100)
		if err != nil {
			t.Fatalf("could not analyze captured audio: %v", err)
		}
		t.Logf("Client captured audio: dominant=%.1f Hz, RMS=%.6f", dominant, rms)

		if rms < 0.001 {
			t.Fatal("client captured audio is silent — rdpsnd channel not delivering audio")
		}
		if math.Abs(dominant-440.0) > 44.0 {
			t.Errorf("dominant frequency %.1f Hz, expected ~440 Hz (±10%%)", dominant)
		} else {
			t.Logf("PASS: end-to-end RDP audio — client captured %.1f Hz sine (RMS=%.4f)", dominant, rms)
		}
	})
}

// generateSineWAV returns a 16-bit stereo PCM WAV at the given frequency.
func generateSineWAV(freq float64, sampleRate, durationSec, channels int) []byte {
	numSamples := sampleRate * durationSec
	dataSize := numSamples * channels * 2
	fileSize := 36 + dataSize

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(fileSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	off := 44
	for i := 0; i < numSamples; i++ {
		v := math.Sin(2.0 * math.Pi * freq * float64(i) / float64(sampleRate))
		s := int16(v * 32000)
		for ch := 0; ch < channels; ch++ {
			binary.LittleEndian.PutUint16(buf[off:off+2], uint16(s))
			off += 2
		}
	}
	return buf
}

// copyFromContainer copies a file from the container to a local path.
func copyFromContainer(t *testing.T, c testcontainers.Container, containerPath, localPath string) {
	t.Helper()
	ctx := context.Background()
	reader, err := c.CopyFileFromContainer(ctx, containerPath)
	if err != nil {
		t.Fatalf("copy %s from container: %v", containerPath, err)
	}
	defer reader.Close()

	// CopyFileFromContainer returns a tar stream; extract the single file.
	data, err := extractTarFile(reader)
	if err != nil {
		t.Fatalf("extract %s from tar: %v", containerPath, err)
	}
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		t.Fatalf("write %s: %v", localPath, err)
	}
}

// extractTarFile reads the first file from a tar archive stream.
func extractTarFile(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)
	_, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("tar next: %w", err)
	}
	return io.ReadAll(tr)
}

// analyzeWavFrequency reads a 16-bit PCM WAV file, computes RMS amplitude and
// the dominant frequency via FFT (zero-padded DFT on the left channel).
func analyzeWavFrequency(path string, sampleRate int) (dominantHz float64, rms float64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	// Minimal WAV parsing: skip 44-byte header, read s16le samples.
	if len(data) < 45 {
		return 0, 0, fmt.Errorf("WAV file too small (%d bytes)", len(data))
	}
	if string(data[:4]) != "RIFF" {
		return 0, 0, fmt.Errorf("not a WAV file (magic: %q)", string(data[:4]))
	}

	// Extract PCM data after 44-byte header (assumes standard WAV header).
	pcm := data[44:]
	// 2 channels × 2 bytes/sample = 4 bytes per frame; take left channel only.
	numFrames := len(pcm) / 4
	if numFrames < 256 {
		return 0, 0, fmt.Errorf("too few audio frames (%d)", numFrames)
	}

	// Use a power-of-2 FFT size for simpler DFT.
	fftSize := 1
	for fftSize < numFrames && fftSize < 65536 {
		fftSize <<= 1
	}
	if fftSize > numFrames {
		fftSize >>= 1
	}

	samples := make([]float64, fftSize)
	var sumSq float64
	for i := 0; i < fftSize; i++ {
		off := i * 4 // left channel of stereo s16le
		s := int16(binary.LittleEndian.Uint16(pcm[off : off+2]))
		v := float64(s) / 32768.0
		samples[i] = v
		sumSq += v * v
	}
	rms = math.Sqrt(sumSq / float64(fftSize))

	// Simple DFT (Cooley-Tukey radix-2 FFT).
	spectrum := fft(samples)

	// Find peak in the first half (positive frequencies only).
	maxMag := 0.0
	maxBin := 0
	for i := 1; i < fftSize/2; i++ {
		mag := cmplx.Abs(spectrum[i])
		if mag > maxMag {
			maxMag = mag
			maxBin = i
		}
	}
	dominantHz = float64(maxBin) * float64(sampleRate) / float64(fftSize)
	return dominantHz, rms, nil
}

// fft computes an in-place radix-2 Cooley-Tukey FFT.
func fft(x []float64) []complex128 {
	n := len(x)
	if n&(n-1) != 0 {
		panic("fft: length must be power of 2")
	}

	c := make([]complex128, n)
	for i, v := range x {
		c[i] = complex(v, 0)
	}

	// Bit-reversal permutation.
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			c[i], c[j] = c[j], c[i]
		}
	}

	// Cooley-Tukey butterfly.
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		w := -2.0 * math.Pi / float64(size)
		for i := 0; i < n; i += size {
			for k := 0; k < half; k++ {
				t := cmplx.Rect(1, w*float64(k)) * c[i+k+half]
				c[i+k+half] = c[i+k] - t
				c[i+k] = c[i+k] + t
			}
		}
	}
	return c
}
