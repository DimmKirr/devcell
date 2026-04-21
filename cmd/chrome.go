package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var (
	chromeSyncOnly  bool
	chromeNoSync    bool
	chromeForce     bool
)

var chromeCmd = &cobra.Command{
	Use:   "chrome [app-name] [-- urls...]",
	Short: "Open Chromium with a project-scoped profile and sync cookies to Playwright",
	Long: `Opens Chromium on the host with a per-app browser profile. Log in to the
sites you need, then press Enter in the terminal. Chromium closes and
cookies are exported as a Playwright storage-state.json so authenticated
sessions carry over to browser automation inside the container.

Each app-name gets its own isolated Chrome profile stored at
~/.devcell/<session>/.chrome/<app-name>/. When only one cell is running
the app-name is optional.

Examples:

    cell chrome tripit                  # open, log in, Enter → sync
    cell chrome tripit -- https://tripit.com
    cell chrome --sync tripit           # re-sync without opening browser
    cell chrome --no-sync tripit        # browse without syncing`,
	Args:              cobra.ArbitraryArgs,
	RunE:              runChrome,
	ValidArgsFunction: completeRunningApps,
}

var loginCmd = &cobra.Command{
	Use:   "login <url>",
	Short: "Open a URL in Chromium, log in, and sync cookies to Playwright",
	Long: `Shortcut for "cell chrome" that opens a specific URL directly.
Opens Chromium, navigates to the URL, waits for you to log in, then
exports cookies as storage-state.json for Playwright MCP.

Examples:

    cell login https://tripit.com
    cell login https://github.com/login`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChrome(cmd, args)
	},
}

func init() {
	chromeCmd.Flags().BoolVar(&chromeSyncOnly, "sync", false, "sync cookies only (don't open browser)")
	chromeCmd.Flags().BoolVar(&chromeNoSync, "no-sync", false, "open browser without syncing cookies on close")
	chromeCmd.Flags().BoolVar(&chromeForce, "force", false, "wipe saved browser profile and force a fresh login")
	loginCmd.Flags().BoolVar(&chromeForce, "force", false, "wipe saved browser profile and force a fresh login")
}

// chromeBinary returns the path to the best available Chromium/Chrome binary.
func chromeBinary() (string, error) {
	if runtime.GOOS == "darwin" {
		candidates := []string{
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		return "", fmt.Errorf("no Chromium or Google Chrome found in /Applications — install one of them")
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no chromium or google-chrome found on PATH")
}

func runChrome(cmd *cobra.Command, args []string) error {
	applyOutputFlags()
	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	appName, urls := parseChromArgs(args)
	if appName == "" {
		appName = c.SessionName
	}

	chromeProfile := filepath.Join(c.CellHome, ".chrome", appName)
	storageStatePath := filepath.Join(c.CellHome, "storage-state.json")

	ux.Debugf("session: %s, cellID: %s, appName: %s", c.SessionName, c.CellID, c.AppName)
	ux.Debugf("chrome profile: %s", chromeProfile)
	ux.Debugf("storage-state: %s", storageStatePath)

	if chromeSyncOnly {
		return fmt.Errorf("--sync requires a running browser; use 'cell chrome' or 'cell login' instead")
	}

	if chromeForce {
		if _, err := os.Stat(chromeProfile); err == nil {
			ux.Info("Wiping saved browser profile for fresh login...")
			if err := os.RemoveAll(chromeProfile); err != nil {
				return fmt.Errorf("wipe profile: %w", err)
			}
		}
	}

	if !chromeSyncOnly {
		if err := openExtractAndClose(chromeProfile, storageStatePath, urls, chromeNoSync); err != nil {
			return err
		}
	}

	if chromeNoSync {
		return nil
	}

	ux.Info("Cookies ready. Use Playwright to browse with your authenticated session.")

	return nil
}

// storageStateCookie matches Playwright's expected cookie format.
type storageStateCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

type storageStateOrigin struct {
	Origin       string              `json:"origin"`
	LocalStorage []localStorageEntry `json:"localStorage"`
}

type localStorageEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type storageState struct {
	Cookies []storageStateCookie  `json:"cookies"`
	Origins []storageStateOrigin  `json:"origins"`
}

// openExtractAndClose opens Chrome for the user to log in (no CDP, no special
// flags — clean session that won't trigger bot detection), waits for Enter,
// closes the login browser, then launches a headless CDP-only instance against
// the same profile to extract cookies via Network.getAllCookies, and closes it.
func openExtractAndClose(profile, storageStatePath string, urls []string, noSync bool) error {
	bin, err := chromeBinary()
	if err != nil {
		return err
	}
	ux.Debugf("browser: %s", bin)

	// Save Chrome's real fingerprint for Patchright so both use the same identity.
	if readPlaywrightFingerprint(filepath.Dir(filepath.Dir(profile))) == nil {
		ensureFingerprint(bin, storageStatePath)
	}

	// Phase 1: login browser — no CDP, no special flags.
	loginArgv := []string{
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
	}
	loginArgv = append(loginArgv, urls...)

	browserName := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(bin))))
	if browserName == "" || browserName == "." {
		browserName = filepath.Base(bin)
	}
	ux.Info(fmt.Sprintf("Opening %s", browserName))
	ux.Debugf("binary: %s", bin)
	ux.Debugf("args: %s", strings.Join(loginArgv, " "))
	ux.Debugf("profile: %s", profile)

	proc := exec.Command(bin, loginArgv...)
	proc.Stdout = os.Stdout
	if ux.Verbose {
		proc.Stderr = os.Stderr
	}
	if err := proc.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	ux.Debugf("PID: %d", proc.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	fmt.Println()
	fmt.Println(ux.StyleWarning.Render(fmt.Sprintf("  Log in to the sites you need, then press %s when done.", ux.StyleBold.Render("Enter"))))

	enterCh := make(chan struct{}, 1)
	go func() {
		bufio.NewReader(os.Stdin).ReadBytes('\n')
		enterCh <- struct{}{}
	}()

	select {
	case <-enterCh:
		fmt.Println()
		ux.Info("Closing browser...")
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			ux.Debugf("SIGTERM failed: %v, sending SIGKILL", err)
			proc.Process.Kill()
		}
		select {
		case <-done:
			ux.Debugf("browser exited gracefully")
		case <-time.After(5 * time.Second):
			ux.Debugf("graceful shutdown timed out, killing")
			proc.Process.Kill()
			<-done
		}

		if !noSync {
			spMsg := "Extracting cookies"
			if len(urls) > 0 {
				spMsg = "Refreshing session and extracting cookies"
			}
			sp := ux.NewProgressSpinner(spMsg)
			count, sites, err := extractCookiesViaCDP(bin, profile, storageStatePath, urls)
			if err != nil {
				sp.Fail(fmt.Sprintf("cookie extraction failed: %v", err))
			} else {
				sp.Success(fmt.Sprintf("Exported %d cookies for %s", count, sites))
			}
		}

	case err := <-done:
		if err != nil {
			ux.Debugf("browser exited: %v", err)
		}
		ux.Info("Browser closed.")
		if !noSync {
			ux.Warn("Browser closed before cookie extraction — no cookies synced.")
		}

	}

	return nil
}

// extractCookiesViaCDP launches a headless Chrome against the same profile with
// --remote-debugging-port, calls Network.getAllCookies via a Node.js WebSocket
// script, writes storage-state.json, then kills the headless instance.
// CDP is safe here: it runs after the login session ends, so bot detection
// (Kasada/Cloudflare) never sees the debugging port.
// If urls is non-empty, the headless browser navigates to urls[0] first so the
// server can re-issue short-lived auth tokens (e.g. Hyatt's 5-min oscar JWT)
// before cookies are extracted.
func extractCookiesViaCDP(bin, profile, storageStatePath string, urls []string) (int, string, error) {
	const cdpPort = "9222"

	// Phase 2: headless CDP browser — same profile, no visible window.
	cdpArgv := []string{
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--headless=new",
		"--remote-debugging-port=" + cdpPort,
		"about:blank",
	}
	ux.Debugf("CDP browser args: %s", strings.Join(cdpArgv, " "))

	cdpProc := exec.Command(bin, cdpArgv...)
	if ux.Verbose {
		cdpProc.Stderr = os.Stderr
	}
	if err := cdpProc.Start(); err != nil {
		return 0, "", fmt.Errorf("start CDP browser: %w", err)
	}
	defer func() {
		cdpProc.Process.Kill()
		cdpProc.Wait()
	}()

	// Wait for CDP to be ready (poll /json/version).
	cdpBase := "http://localhost:" + cdpPort
	var wsURL string
	for i := 0; i < 20; i++ {
		time.Sleep(300 * time.Millisecond)
		data, err := cdpGet(cdpBase + "/json")
		if err != nil {
			ux.Debugf("CDP not ready yet: %v", err)
			continue
		}
		var targets []struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			Type                 string `json:"type"`
		}
		if err := json.Unmarshal(data, &targets); err != nil {
			continue
		}
		for _, t := range targets {
			if t.Type == "page" && t.WebSocketDebuggerURL != "" {
				wsURL = t.WebSocketDebuggerURL
				break
			}
		}
		if wsURL != "" {
			break
		}
	}
	if wsURL == "" {
		return 0, "", fmt.Errorf("CDP not ready after timeout")
	}
	ux.Debugf("CDP WebSocket: %s", wsURL)

	return extractCookiesViaScript(wsURL, storageStatePath, urls)
}

// cdpGet performs an HTTP GET to the CDP endpoint.
func cdpGet(url string) ([]byte, error) {
	out, err := exec.Command("curl", "-sf", "--max-time", "2", url).Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// extractCookiesViaScript uses a Node.js one-liner to connect to Chrome CDP
// WebSocket and extract all cookies via Network.getAllCookies.
// If urls is non-empty, it navigates to urls[0] first so the server can
// re-issue short-lived auth tokens before the cookie snapshot is taken.
func extractCookiesViaScript(wsURL, dstPath string, urls []string) (int, string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return 0, "", fmt.Errorf("node not found (required for CDP cookie extraction): %w", err)
	}
	ux.Debugf("using node: %s", nodePath)

	navigateTo := ""
	if len(urls) > 0 {
		navigateTo = urls[0]
	}

	// Node.js 22+ has built-in WebSocket — no npm packages needed.
	// Extracts cookies + localStorage from the active Chrome profile via CDP.
	// If navigateTo is set: enables Page events, navigates to the URL, waits for
	// loadEventFired so the server can refresh short-lived tokens (e.g. Hyatt oscar),
	// then extracts cookies AND localStorage for every frame origin on the page.
	// Output is Playwright storage-state JSON format (cookies + origins[].localStorage).
	script := fmt.Sprintf(`
const ws = new WebSocket(%q);
const navigateTo = %q;

let cookies = null;
let origins = null;   // set after Page.getFrameTree response
let lsData = {};      // origin -> [{name,value}]
let lsPending = 0;    // outstanding DOMStorage requests

function tryDone() {
  if (cookies === null || origins === null || lsPending > 0) return;
  const state = {
    cookies,
    origins: origins
      .filter(o => lsData[o] && lsData[o].length > 0)
      .map(o => ({origin: o, localStorage: lsData[o]}))
  };
  process.stdout.write(JSON.stringify(state));
  ws.close();
}

function fetchAll() {
  ws.send(JSON.stringify({id:20, method:'Network.getAllCookies'}));
  if (navigateTo) {
    ws.send(JSON.stringify({id:15, method:'DOMStorage.enable'}));
    ws.send(JSON.stringify({id:30, method:'Page.getFrameTree'}));
  } else {
    origins = [];
    tryDone();
  }
}

ws.onopen = () => {
  if (navigateTo) {
    ws.send(JSON.stringify({id:1, method:'Page.enable'}));
  } else {
    fetchAll();
  }
};

ws.onmessage = (event) => {
  const m = JSON.parse(event.data);

  if (m.id === 1) {
    // Page.enable done — navigate; 20s safety fallback if load event never fires.
    ws.send(JSON.stringify({id:2, method:'Page.navigate', params:{url:navigateTo}}));
    setTimeout(() => { if (cookies === null) fetchAll(); }, 20000);
    return;
  }

  if (m.method === 'Page.loadEventFired') {
    fetchAll();
    return;
  }

  if (m.id === 20) {
    // Network.getAllCookies response
    const raw = (m.result && m.result.cookies) || [];
    cookies = raw.map(c => {
      const ss = c.sameSite || 'Lax';
      const secure = (ss === 'None') ? true : !!c.secure;
      return {
        name: c.name, value: c.value,
        domain: c.domain,
        path: c.path,
        expires: c.expires === -1 ? -1 : c.expires,
        httpOnly: !!c.httpOnly, secure,
        sameSite: (!secure && ss === 'None') ? 'Lax' : ss
      };
    });
    tryDone();
    return;
  }

  if (m.id === 30 && m.result) {
    // Page.getFrameTree — collect unique https/http origins from all frames.
    const seen = new Set();
    function collect(node) {
      if (node && node.frame && node.frame.url) {
        try {
          const u = new URL(node.frame.url);
          if ((u.protocol === 'https:' || u.protocol === 'http:') && !seen.has(u.origin)) {
            seen.add(u.origin);
          }
        } catch(e) {}
      }
      (node.childFrames || []).forEach(collect);
    }
    collect(m.result.frameTree);
    origins = [...seen];
    lsPending = origins.length;
    if (lsPending === 0) { tryDone(); return; }
    origins.forEach((o, i) => {
      ws.send(JSON.stringify({id: 100+i, method:'DOMStorage.getDOMStorageItems',
        params:{storageId:{securityOrigin:o, isLocalStorage:true}}}));
    });
    return;
  }

  if (m.id >= 100 && origins && m.id < 100 + origins.length) {
    // DOMStorage.getDOMStorageItems response for origins[id-100].
    const i = m.id - 100;
    const entries = (m.result && m.result.entries) || [];
    lsData[origins[i]] = entries.map(([name, value]) => ({name, value}));
    lsPending--;
    tryDone();
    return;
  }
};

ws.onerror = (e) => { process.stderr.write(String(e.message||e)); process.exit(1); };
`, wsURL, navigateTo)

	cmd := exec.Command(nodePath, "-e", script)
	if ux.Verbose {
		cmd.Stderr = os.Stderr
	}
	out, err := cmd.Output()
	if err != nil {
		return 0, "", fmt.Errorf("CDP script failed: %w", err)
	}

	var state storageState
	if err := json.Unmarshal(out, &state); err != nil {
		return 0, "", fmt.Errorf("invalid CDP output: %w", err)
	}

	tmpFile := dstPath + ".tmp"
	formatted, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(tmpFile, formatted, 0600); err != nil {
		return 0, "", fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmpFile, dstPath); err != nil {
		os.Remove(tmpFile)
		return 0, "", fmt.Errorf("rename: %w", err)
	}

	domainSet := make(map[string]bool)
	for _, c := range state.Cookies {
		domainSet[c.Domain] = true
	}
	var domains []string
	for d := range domainSet {
		domains = append(domains, d)
	}
	return len(state.Cookies), strings.Join(domains, ", "), nil
}

// parseChromArgs splits positional args into an optional app-name and URLs.
func parseChromArgs(args []string) (appName string, urls []string) {
	for i, a := range args {
		if a == "--" {
			urls = args[i+1:]
			return
		}
		if len(appName) == 0 && !isURL(a) {
			appName = a
		} else {
			urls = append(urls, a)
		}
	}
	return
}

func isURL(s string) bool {
	return len(s) > 8 && (s[:7] == "http://" || s[:8] == "https://")
}

const fingerprintFile = "playwright-fingerprint.json"

// playwrightFingerprint holds the full browser fingerprint saved for Patchright.
type playwrightFingerprint struct {
	UserAgent  string    `json:"userAgent"`
	Platform   string    `json:"platform"`   // "MacIntel"
	UAPlatform string    `json:"uaPlatform"` // "macOS"
	Version    string    `json:"version"`    // e.g. "147.0.7453.0"
	Brands     []fpBrand `json:"brands"`
}

type fpBrand struct {
	Brand   string `json:"brand"`
	Version string `json:"version"`
}

// chromeFingerprint runs `<bin> --version` to get the real version (e.g. "Google Chrome 147.0.7453.0")
// and builds a full macOS fingerprint. Chrome always reports 10_15_7 regardless of actual macOS
// version — that's Chrome's own fingerprinting behaviour, not a spoof.
// Returns nil on error.
func chromeFingerprint(bin string) *playwrightFingerprint {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return nil
	}
	// Output: "Google Chrome 147.0.7453.0\n" or "Chromium 147.0.7453.0\n"
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) == 0 {
		return nil
	}
	version := parts[len(parts)-1]
	major := version
	if idx := strings.Index(version, "."); idx >= 0 {
		major = version[:idx]
	}
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + version + " Safari/537.36"
	return &playwrightFingerprint{
		UserAgent:  ua,
		Platform:   "MacIntel",
		UAPlatform: "macOS",
		Version:    version,
		Brands: []fpBrand{
			{Brand: "Google Chrome", Version: major},
			{Brand: "Chromium", Version: major},
			{Brand: "Not/A)Brand", Version: "8"},
		},
	}
}

func ensureFingerprint(bin, storageStatePath string) *playwrightFingerprint {
	cellHome := filepath.Dir(storageStatePath)
	fp := chromeFingerprint(bin)
	if fp == nil {
		// Fallback: generic recent macOS Chrome fingerprint — matches Client Hints platform.
		fp = &playwrightFingerprint{
			UserAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36",
			Platform:   "MacIntel",
			UAPlatform: "macOS",
			Version:    "147.0.0.0",
			Brands: []fpBrand{
				{Brand: "Google Chrome", Version: "147"},
				{Brand: "Chromium", Version: "147"},
				{Brand: "Not/A)Brand", Version: "8"},
			},
		}
	}
	ux.Debugf("fingerprint UA: %s", fp.UserAgent)
	savePlaywrightFingerprint(cellHome, fp)
	return fp
}

func readPlaywrightFingerprint(cellHome string) *playwrightFingerprint {
	data, err := os.ReadFile(filepath.Join(cellHome, fingerprintFile))
	if err != nil {
		return nil
	}
	var fp playwrightFingerprint
	if err := json.Unmarshal(data, &fp); err != nil {
		return nil
	}
	if fp.UserAgent == "" {
		return nil
	}
	return &fp
}

func savePlaywrightFingerprint(cellHome string, fp *playwrightFingerprint) {
	data, _ := json.MarshalIndent(fp, "", "  ")
	path := filepath.Join(cellHome, fingerprintFile)
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return
	}
	os.Rename(tmpFile, path)
}
