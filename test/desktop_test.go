package container_test

// desktop_test.go — Tests for fluxbox desktop theme, taskbar, and menu styling.
//
// Verifies that nixhome/modules/desktop/theme.nix values are deployed
// correctly inside the container at /opt/devcell/.fluxbox/.
//
// Run against a GUI-capable image:
//   DEVCELL_TEST_IMAGE=ghcr.io/dimmkirr/devcell:latest-ultimate go test -v -run TestDesktop ./...

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	devcellHome = "/opt/devcell"
	themePath   = devcellHome + "/.fluxbox/styles/devcell-ocean/theme.cfg"
	overlayPath = devcellHome + "/.fluxbox/overlay"
	initPath    = devcellHome + "/.fluxbox/init"
)

// assertContains checks that content contains the expected substring.
func assertContains(t *testing.T, label, content, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Errorf("%s: expected %q not found in:\n%s", label, expected, content)
	}
}

// ── Theme file existence ────────────────────────────────────────────────────

// TestDesktopThemeFileExists — theme.cfg must be deployed at the expected path.
func TestDesktopThemeFileExists(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	for _, path := range []string{themePath, overlayPath, initPath} {
		out, code := exec(t, c, []string{"test", "-f", path})
		if code != 0 {
			t.Errorf("FAIL: %s does not exist (exit %d): %s", path, code, out)
		} else {
			t.Logf("PASS: %s exists", path)
		}
	}
}

// TestDesktopWallpaperExists — wallpaper.png must be deployed.
func TestDesktopWallpaperExists(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	path := devcellHome + "/.fluxbox/wallpaper.png"
	_, code := exec(t, c, []string{"test", "-f", path})
	if code != 0 {
		t.Fatalf("FAIL: wallpaper not found at %s", path)
	}
	// Verify it's a valid PNG (magic bytes)
	out, code := exec(t, c, []string{"sh", "-c", "head -c 4 " + path + " | od -A n -t x1 | tr -d ' '"})
	if code != 0 {
		t.Fatalf("FAIL: could not read wallpaper header: %s", out)
	}
	if !strings.Contains(out, "89504e47") {
		t.Errorf("FAIL: wallpaper is not a valid PNG (magic: %s)", out)
	} else {
		t.Logf("PASS: wallpaper.png exists and is a valid PNG")
	}
}

// ── Toolbar styling ─────────────────────────────────────────────────────────

// TestDesktopToolbarHeight — toolbar must be 35px.
func TestDesktopToolbarHeight(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})
	assertContains(t, "toolbar.height", theme, "toolbar.height:  35")
}

// TestDesktopToolbarColors — toolbar uses the correct palette colors.
func TestDesktopToolbarColors(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	// Toolbar background = border color (#000000)
	assertContains(t, "toolbar.color", theme, "toolbar.color:  #000000")
	// Clock text = textBright (#ffffff)
	assertContains(t, "toolbar.clock.textColor", theme, "toolbar.clock.textColor:  #ffffff")
	// Workspace badge = highlight (#b8e336)
	assertContains(t, "toolbar.workspace.color", theme, "toolbar.workspace.color:  #b8e336")
	// Focused iconbar = raised (#1a1a2e)
	assertContains(t, "toolbar.iconbar.focused.color", theme, "toolbar.iconbar.focused.color:  #1a1a2e")
	// Unfocused text = inactive (#667788)
	assertContains(t, "toolbar.iconbar.unfocused.textColor", theme, "toolbar.iconbar.unfocused.textColor:  #667788")
}

// TestDesktopToolbarFonts — toolbar uses JetBrainsMono Nerd Font at correct sizes.
func TestDesktopToolbarFonts(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	// Clock font = JetBrainsMono Nerd Font-9:bold
	assertContains(t, "toolbar.clock.font", theme, "toolbar.clock.font:  JetBrainsMono Nerd Font-9:bold")
	// Workspace font = JetBrainsMono Nerd Font-9:bold
	assertContains(t, "toolbar.workspace.font", theme, "toolbar.workspace.font:  JetBrainsMono Nerd Font-9:bold")
	// Focused iconbar font = JetBrainsMono Nerd Font-9:bold (same size as unfocused for baseline alignment)
	assertContains(t, "toolbar.iconbar.focused.font", theme, "toolbar.iconbar.focused.font:  JetBrainsMono Nerd Font-9:bold")
}

// ── Menu styling ────────────────────────────────────────────────────────────

// TestDesktopMenuTitle — menu title uses highlight color, large bold font, left-justified.
func TestDesktopMenuTitle(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	// Title background = highlight (#b8e336)
	assertContains(t, "menu.title.color", theme, "menu.title.color:  #b8e336")
	// Title text = border (#000000)
	assertContains(t, "menu.title.textColor", theme, "menu.title.textColor:  #000000")
	// Title font = JetBrainsMono Nerd Font-13:bold
	assertContains(t, "menu.title.font", theme, "menu.title.font:  JetBrainsMono Nerd Font-13:bold")
	// Title justify = left
	assertContains(t, "menu.title.justify", theme, "menu.title.justify:  left")
	// Title height = 32
	assertContains(t, "menu.titleHeight", theme, "menu.titleHeight:  32")
}

// TestDesktopMenuBody — menu body uses surface color, white text, correct item height.
func TestDesktopMenuBody(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	// Body background = surface (#0a0a18)
	assertContains(t, "menu.frame.color", theme, "menu.frame.color:  #0a0a18")
	// Body text = textBright (#ffffff)
	assertContains(t, "menu.frame.textColor", theme, "menu.frame.textColor:  #ffffff")
	// Body font = JetBrainsMono Nerd Font-11
	assertContains(t, "menu.frame.font", theme, "menu.frame.font:  JetBrainsMono Nerd Font-11")
	// Item height = 28
	assertContains(t, "menu.itemHeight", theme, "menu.itemHeight:  28")
	// Highlight = highlight (#b8e336)
	assertContains(t, "menu.hilite.color", theme, "menu.hilite.color:  #b8e336")
	// Border = 3px black (neobrutalist)
	assertContains(t, "menu.borderWidth", theme, "menu.borderWidth:  3")
	assertContains(t, "menu.borderColor", theme, "menu.borderColor:  #000000")
}

// ── Window styling ──────────────────────────────────────────────────────────

// TestDesktopWindowBorder — windows must have thick 3px black border (neobrutalist signature).
func TestDesktopWindowBorder(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	assertContains(t, "window.borderWidth", theme, "window.borderWidth:  3")
	assertContains(t, "window.borderColor", theme, "window.borderColor:  #000000")
}

// TestDesktopWindowTitle — focused window title bar must be 30px, flat, black bg.
func TestDesktopWindowTitle(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	assertContains(t, "window.title.height", theme, "window.title.height:  30")
	assertContains(t, "window.title.focus", theme, "window.title.focus:  flat")
	assertContains(t, "window.title.focus.color", theme, "window.title.focus.color:  #000000")
	// Title text font = JetBrainsMono Nerd Font-10:bold
	assertContains(t, "window.label.focus.font", theme, "window.label.focus.font:  JetBrainsMono Nerd Font-10:bold")
	// Title text color = text (#f0f0f0)
	assertContains(t, "window.label.focus.textColor", theme, "window.label.focus.textColor:  #f0f0f0")
}

// TestDesktopWindowHandle — focused handle = highlight green, 10px width.
func TestDesktopWindowHandle(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	assertContains(t, "window.handleWidth", theme, "window.handleWidth:  10")
	assertContains(t, "window.handle.focus.color", theme, "window.handle.focus.color:  #b8e336")
}

// ── Pixmaps ─────────────────────────────────────────────────────────────────

// TestDesktopPixmapsExist — all 6 window button pixmaps must be deployed.
func TestDesktopPixmapsExist(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	pixmapDir := devcellHome + "/.fluxbox/styles/devcell-ocean/pixmaps"
	pixmaps := []string{
		"close.xpm", "close_unfocus.xpm",
		"max.xpm", "max_unfocus.xpm",
		"min.xpm", "min_unfocus.xpm",
	}
	for _, p := range pixmaps {
		path := pixmapDir + "/" + p
		_, code := exec(t, c, []string{"test", "-f", path})
		if code != 0 {
			t.Errorf("FAIL: pixmap %s not found", path)
		} else {
			t.Logf("PASS: %s exists", p)
		}
	}
}

// TestDesktopPixmapReferences — theme.cfg must reference the pixmap files.
func TestDesktopPixmapReferences(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})

	assertContains(t, "window.close.pixmap", theme, "window.close.pixmap:  pixmaps/close.xpm")
	assertContains(t, "window.maximize.pixmap", theme, "window.maximize.pixmap:  pixmaps/max.xpm")
	assertContains(t, "window.iconify.pixmap", theme, "window.iconify.pixmap:  pixmaps/min.xpm")
}

// ── Init / session config ───────────────────────────────────────────────────

// TestDesktopInitConfig — fluxbox init must reference the correct style and menu paths.
func TestDesktopInitConfig(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	init, _ := exec(t, c, []string{"cat", initPath})

	assertContains(t, "styleFile", init, "session.styleFile:\t"+devcellHome+"/.fluxbox/styles/devcell-ocean/theme.cfg")
	assertContains(t, "menuFile", init, "session.menuFile:\t"+devcellHome+"/.fluxbox/menu")
	assertContains(t, "overlay", init, "session.styleOverlay:\t"+devcellHome+"/.fluxbox/overlay")
	assertContains(t, "toolbar.placement", init, "session.screen0.toolbar.placement: BottomCenter")
	assertContains(t, "toolbar.widthPercent", init, "session.screen0.toolbar.widthPercent: 100")
	assertContains(t, "toolbar.visible", init, "session.screen0.toolbar.visible: true")
}

// TestDesktopOverlayMatchesTheme — overlay must contain the same content as theme.cfg.
func TestDesktopOverlayMatchesTheme(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	theme, _ := exec(t, c, []string{"cat", themePath})
	overlay, _ := exec(t, c, []string{"cat", overlayPath})

	if theme != overlay {
		t.Errorf("FAIL: overlay content differs from theme.cfg\ntheme.cfg length: %d\noverlay length: %d", len(theme), len(overlay))
	} else {
		t.Logf("PASS: overlay matches theme.cfg (%d bytes)", len(theme))
	}
}

// ── Xresources ──────────────────────────────────────────────────────────────

// TestDesktopXresources — Xresources must set XTerm colors from the palette.
func TestDesktopXresources(t *testing.T) {
	c := startEnvContainer(t)
	skipIfNoGUI(t, c)

	xres, _ := exec(t, c, []string{"cat", devcellHome + "/.Xresources"})

	// Background = surface (#0a0a18)
	assertContains(t, "XTerm*background", xres, "XTerm*background:       #0a0a18")
	// Cursor = accent (#1abc9c)
	assertContains(t, "XTerm*cursorColor", xres, "XTerm*cursorColor:      #1abc9c")
	// Font = JetBrainsMono Nerd Font
	assertContains(t, "XTerm*faceName", xres, "XTerm*faceName:         JetBrainsMono Nerd Font")
	// Font size = 11
	assertContains(t, "XTerm*faceSize", xres, "XTerm*faceSize:         11")
}

// ── Patchright stealth MCP ───────────────────────────────────────────────────

// TestDesktopPatchrightMcpAvailable verifies mcp-server-patchright binary is on PATH.
// The npm package is "patchright-mcp" but its bin name is "mcp-server-patchright".
func TestDesktopPatchrightMcpAvailable(t *testing.T) {
	c := startContainer(t, map[string]string{"HOST_USER": hostUser, "APP_NAME": "test"})

	out, code := exec(t, c, []string{"sh", "-c", "command -v mcp-server-patchright"})
	if code != 0 {
		t.Fatalf("FAIL: mcp-server-patchright not on PATH (exit %d)", code)
	}
	t.Logf("PASS: %s", strings.TrimSpace(out))
}

// TestDesktopStealthInitScript verifies the stealth init-script exists in nix store.
func TestDesktopStealthInitScript(t *testing.T) {
	c := startContainer(t, map[string]string{"HOST_USER": hostUser, "APP_NAME": "test"})

	out, code := exec(t, c, []string{"sh", "-c",
		"ls -1 /nix/store/*stealth-init.js 2>/dev/null | head -1"})
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("FAIL: stealth-init.js not found in nix store")
	}
	// Verify key stealth patches are present
	content, _ := exec(t, c, []string{"cat", strings.TrimSpace(out)})
	if !strings.Contains(content, "navigator.webdriver") {
		t.Errorf("FAIL: stealth-init.js missing webdriver patch")
	}
	if !strings.Contains(content, "Intel Inc.") {
		t.Errorf("FAIL: stealth-init.js missing WebGL spoof")
	}
	t.Logf("PASS: stealth-init.js found at %s", strings.TrimSpace(out))
}

// TestDesktopPatchrightMcpCellWrapper verifies the patchright-mcp-cell wrapper exists
// and references patchright-mcp (not playwright-mcp).
func TestDesktopPatchrightMcpCellWrapper(t *testing.T) {
	c := startContainer(t, map[string]string{"HOST_USER": hostUser, "APP_NAME": "test"})

	out, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"})
	if code != 0 {
		t.Fatalf("FAIL: patchright-mcp-cell not on PATH (exit %d)", code)
	}
	// Read the wrapper and verify it calls mcp-server-patchright
	wrapper, _ := exec(t, c, []string{"cat", strings.TrimSpace(out)})
	if !strings.Contains(wrapper, "mcp-server-patchright") {
		t.Errorf("FAIL: wrapper does not call mcp-server-patchright")
	}
	if strings.Contains(wrapper, "playwright-mcp ") {
		t.Errorf("FAIL: wrapper still references playwright-mcp")
	}
	t.Logf("PASS: patchright-mcp-cell wrapper found at %s", strings.TrimSpace(out))
}

// ── Runtime GUI tests (require full GUI stack) ──────────────────────────────

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

// TestDesktopXresourcesLoaded — with GUI enabled, xrdb must have loaded the
// Xresources into the X server's resource database at startup.
// xrdb loading is deferred (background process after entrypoint exec gosu)
// so we retry for up to 5 seconds.
func TestDesktopXresourcesLoaded(t *testing.T) {
	probeGUI(t)
	c := startDesktopGUIContainer(t)

	// xrdb is loaded by a deferred background process (~1s after entrypoint);
	// retry for up to 5 seconds to allow it to complete.
	var out string
	var code int
	for i := 0; i < 10; i++ {
		out, code = exec(t, c, []string{"sh", "-c", "DISPLAY=:99 xrdb -query"})
		if code == 0 && out != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if code != 0 {
		t.Fatalf("xrdb -query failed (exit %d): %s", code, out)
	}
	if out == "" {
		t.Fatal("FAIL: X resource database is empty — Xresources not loaded at startup")
	}

	// Verify key resources from theme.nix palette
	assertContains(t, "XTerm*background", out, "XTerm*background:\t#0a0a18")
	assertContains(t, "XTerm*cursorColor", out, "XTerm*cursorColor:\t#1abc9c")
	assertContains(t, "XTerm*faceName", out, "XTerm*faceName:\tJetBrainsMono Nerd Font")
	assertContains(t, "XTerm*faceSize", out, "XTerm*faceSize:\t11")
	t.Logf("PASS: Xresources loaded into X server (%d lines)", strings.Count(out, "\n")+1)
}

// TestDesktopFluxboxThemeActive — with GUI enabled, fluxbox must be running
// with the devcell-ocean theme (not default grey).
func TestDesktopFluxboxThemeActive(t *testing.T) {
	probeGUI(t)
	c := startDesktopGUIContainer(t)

	// Verify fluxbox is running with our patched init
	out, code := exec(t, c, []string{"sh", "-c", "cat /tmp/fluxbox-init"})
	if code != 0 {
		t.Fatalf("could not read /tmp/fluxbox-init (exit %d): %s", code, out)
	}
	// The init must point to our custom theme
	assertContains(t, "styleFile", out,
		"session.styleFile:\t"+devcellHome+"/.fluxbox/styles/devcell-ocean/theme.cfg")
	// Workspace name must be patched with APP_NAME
	assertContains(t, "workspaceNames", out, "test")
	t.Logf("PASS: fluxbox running with devcell-ocean theme, workspace='test'")
}

// TestDesktopXftDpi96 — Xft.dpi must be set to 96 in X resources so that
// Chromium reports devicePixelRatio=1 and window.screen matches the Xvfb
// resolution (1920x1080). Without this, Xvfb reports 0mm x 0mm physical
// size, causing Chromium to calculate a non-1.0 DPR (~1.047) and report
// the screen as ~1835x1032 instead of 1920x1080.
func TestDesktopXftDpi96(t *testing.T) {
	probeGUI(t)
	c := startDesktopGUIContainer(t)

	// Wait for xrdb to load (deferred background process, ~1s)
	var out string
	var code int
	for i := 0; i < 10; i++ {
		out, code = exec(t, c, []string{"sh", "-c", "DISPLAY=:99 xrdb -query 2>&1"})
		if code == 0 && strings.Contains(out, "Xft.dpi") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(out, "Xft.dpi") {
		t.Fatalf("FAIL: Xft.dpi not found in X resource database:\n%s", out)
	}
	assertContains(t, "Xft.dpi", out, "Xft.dpi:\t96")
	t.Logf("PASS: Xft.dpi set to 96 in X resources")
}

// TestDesktopXvfbDpi96 — Xvfb must be started with -dpi 96 for consistent
// rendering across all X11 clients, not just those reading Xft.dpi.
func TestDesktopXvfbDpi96(t *testing.T) {
	probeGUI(t)
	c := startDesktopGUIContainer(t)

	out, code := exec(t, c, []string{"sh", "-c",
		"cat /proc/$(pgrep Xvfb)/cmdline 2>/dev/null | tr '\\0' ' '"})
	if code != 0 {
		t.Fatalf("FAIL: could not read Xvfb cmdline (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "-dpi 96") && !strings.Contains(out, "-dpi96") {
		t.Errorf("FAIL: Xvfb not started with -dpi 96:\n%s", out)
	} else {
		t.Logf("PASS: Xvfb started with -dpi 96")
	}
}

// TestDesktopScreenshotNotDefaultGrey — capture a screenshot and verify
// the taskbar is not the default fluxbox grey (#bebebd) but our black (#000000).
// Uses ImageMagick's `import` to capture the X root window.
func TestDesktopScreenshotNotDefaultGrey(t *testing.T) {
	probeGUI(t)
	c := startDesktopGUIContainer(t)

	// Capture a screenshot
	_, code := exec(t, c, []string{"sh", "-c", "DISPLAY=:99 import -window root /tmp/desktop.png"})
	if code != 0 {
		t.Skip("import (ImageMagick) not available — skipping screenshot test")
	}

	// Sample the taskbar area (bottom 40px strip, center pixel)
	// Use ImageMagick to get the color of a pixel at (960, 1060) — middle of the toolbar
	// on a 1920x1080 screen with 40px toolbar at bottom.
	out, code := exec(t, c, []string{"sh", "-c",
		"DISPLAY=:99 convert /tmp/desktop.png -crop 1x1+960+1060 -format '%[hex:u.p{0,0}]' info:"})
	if code != 0 {
		t.Skipf("convert not available (exit %d): %s", code, out)
	}

	// The toolbar pixel should be dark (our theme: #000000) not default grey (#bebebd)
	out = strings.TrimSpace(strings.ToUpper(out))
	t.Logf("Toolbar pixel color at (960,1060): #%s", out)

	// Default fluxbox grey is around BEBEBD; our theme is 000000.
	// Allow for some variation but it should definitely not be light grey.
	if strings.HasPrefix(out, "BE") || strings.HasPrefix(out, "C0") || strings.HasPrefix(out, "D0") {
		t.Errorf("FAIL: toolbar appears to use default grey color (#%s) — theme not applied", out)
	} else {
		t.Logf("PASS: toolbar is not default grey (#%s)", out)
	}
}
