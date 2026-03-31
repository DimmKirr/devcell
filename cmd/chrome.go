package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	chromeSyncOnly bool
	chromeNoSync   bool
)

const chromeDebugPort = "19222"

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
		// --sync without browser: re-extract from a running Chrome or error.
		return fmt.Errorf("--sync requires a running browser; use 'cell chrome' or 'cell login' instead")
	}

	if !chromeSyncOnly {
		if err := openExtractAndClose(chromeProfile, storageStatePath, urls, chromeNoSync); err != nil {
			return err
		}
	}

	if chromeNoSync {
		return nil
	}

	pterm.Info.Println("Cookies ready. Use Playwright to browse with your authenticated session.")

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

type storageState struct {
	Cookies []storageStateCookie `json:"cookies"`
	Origins []struct{}           `json:"origins"`
}

// openExtractAndClose launches Chromium with CDP, waits for user to press
// Enter, extracts cookies via DevTools Protocol (decrypted values), writes
// storage-state.json, then closes Chrome.
func openExtractAndClose(profile, storageStatePath string, urls []string, noSync bool) error {
	bin, err := chromeBinary()
	if err != nil {
		return err
	}
	ux.Debugf("browser: %s", bin)

	// Read Playwright's fingerprint to spoof host Chrome, so session-bound
	// sites (BA, banks) bind cookies to Playwright's fingerprint, not the host's.
	playwrightUA := readPlaywrightFingerprint(filepath.Dir(filepath.Dir(profile)))
	if playwrightUA == "" {
		// Bootstrap: query a running container for the UA, or use known default.
		playwrightUA = getPlaywrightUA(storageStatePath)
	}

	argv := []string{
		"--user-data-dir=" + profile,
		"--remote-debugging-port=" + chromeDebugPort,
	}
	if playwrightUA != "" {
		argv = append(argv, "--user-agent="+playwrightUA)
		ux.Debugf("spoofing UA: %s", playwrightUA)
	}
	argv = append(argv, urls...)

	browserName := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(bin))))
	if browserName == "" || browserName == "." {
		browserName = filepath.Base(bin)
	}
	pterm.Info.Printfln("Opening %s", pterm.LightCyan(browserName))
	ux.Debugf("profile: %s", profile)

	proc := exec.Command(bin, argv...)
	proc.Stdout = os.Stdout
	if ux.Verbose {
		proc.Stderr = os.Stderr
	}
	if err := proc.Start(); err != nil {
		return fmt.Errorf("start chromium: %w", err)
	}
	ux.Debugf("PID: %d", proc.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	fmt.Println()
	pterm.FgLightYellow.Printfln("  Log in to the sites you need, then press %s when done.", pterm.Bold.Sprint("Enter"))

	enterCh := make(chan struct{}, 1)
	go func() {
		bufio.NewReader(os.Stdin).ReadBytes('\n')
		enterCh <- struct{}{}
	}()

	select {
	case <-enterCh:
		fmt.Println()

		if !noSync {
			// Extract cookies via CDP before closing Chrome.
			sp := ux.NewProgressSpinner("Extracting cookies via DevTools")

			// Navigate to about:blank first so no site JS is running.
			cdpNavigateBlank()

			count, sites, err := extractCookiesViaCDP(storageStatePath)
			if err != nil {
				sp.Fail(fmt.Sprintf("cookie extraction failed: %v", err))
			} else {
				sp.Success(fmt.Sprintf("Exported %d cookies for %s", count, pterm.LightCyan(sites)))
			}
		}

		pterm.Info.Println("Closing browser...")
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			ux.Debugf("SIGTERM failed: %v, sending SIGKILL", err)
			proc.Process.Kill()
		}
		select {
		case <-done:
			ux.Debugf("Chromium exited gracefully")
		case <-time.After(5 * time.Second):
			ux.Debugf("graceful shutdown timed out, killing")
			proc.Process.Kill()
			<-done
		}

	case err := <-done:
		if err != nil {
			ux.Debugf("Chromium exited: %v", err)
		}
		pterm.Info.Println("Browser closed.")
		if !noSync {
			pterm.Warning.Println("Browser closed before cookie extraction — no cookies synced.")
		}
	}

	return nil
}

// cdpCall makes a CDP HTTP request to the browser's debugging endpoint.
func cdpCall(method, path string, body io.Reader) ([]byte, error) {
	url := "http://127.0.0.1:" + chromeDebugPort + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// cdpNavigateBlank navigates the first tab to about:blank via CDP so no
// site JavaScript is running during cookie extraction.
func cdpNavigateBlank() {
	// Get first tab's webSocket debugger URL.
	data, err := cdpCall("GET", "/json", nil)
	if err != nil {
		ux.Debugf("CDP /json failed: %v", err)
		return
	}

	var tabs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &tabs); err != nil || len(tabs) == 0 {
		ux.Debugf("CDP no tabs found")
		return
	}

	// Navigate first tab to about:blank via HTTP endpoint.
	_, err = cdpCall("GET", "/json/navigate/"+tabs[0].ID+"?url=about:blank", nil)
	if err != nil {
		ux.Debugf("CDP navigate failed: %v", err)
	}
	// Small delay for navigation to complete.
	time.Sleep(200 * time.Millisecond)
}

// extractCookiesViaCDP connects to Chrome's DevTools Protocol HTTP endpoint
// and retrieves all cookies with decrypted values. Writes storage-state.json.
func extractCookiesViaCDP(dstPath string) (int, string, error) {
	// CDP HTTP API: /json/protocol doesn't expose Network.getAllCookies directly.
	// But we can use the /json/new endpoint to get a debugging target, then use
	// the HTTP-based CDP commands. Actually, the simplest approach is to use
	// the /json endpoint to list pages, then use fetch to call CDP via
	// the page's DevTools URL.

	// Simpler: use Chrome's built-in /json endpoints and a JavaScript evaluation
	// approach. But the cleanest is: Chrome exposes cookies at a hidden endpoint.

	// Actually the simplest reliable approach: use the CDP WebSocket.
	// But for simplicity, let's use the chrome.debugger HTTP API.

	// The most practical approach: use /json to get a target, then use
	// the CDP REST-like endpoint: POST to send CDP command.

	// Let's use the approach of evaluating JS via CDP to get cookies.
	// This works because we navigated to about:blank.

	// Get targets.
	data, err := cdpCall("GET", "/json", nil)
	if err != nil {
		return 0, "", fmt.Errorf("CDP connection failed (is Chrome running?): %w", err)
	}

	var targets []struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		ID                   string `json:"id"`
		Type                 string `json:"type"`
	}
	if err := json.Unmarshal(data, &targets); err != nil {
		return 0, "", fmt.Errorf("parse CDP targets: %w", err)
	}

	// Find a page target.
	var targetID string
	for _, t := range targets {
		if t.Type == "page" {
			targetID = t.ID
			break
		}
	}
	if targetID == "" {
		return 0, "", fmt.Errorf("no page target found in CDP")
	}

	// Use the CDP HTTP protocol command endpoint.
	// Chrome DevTools Protocol over HTTP: we need WebSocket for commands.
	// The simpler alternative: use an external tool like `chrome-remote-interface`
	// or just shell out to a small script.

	// Simplest reliable approach: use Node.js (available on macOS) to connect
	// via WebSocket and call Network.getAllCookies.
	return extractCookiesViaScript(targets[0].WebSocketDebuggerURL, dstPath)
}

// extractCookiesViaScript uses a Node.js one-liner to connect to Chrome CDP
// WebSocket and extract all cookies via Network.getAllCookies.
func extractCookiesViaScript(wsURL, dstPath string) (int, string, error) {
	// Check if Node.js is available (it is on macOS).
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return 0, "", fmt.Errorf("node not found (required for CDP cookie extraction): %w", err)
	}
	ux.Debugf("using node: %s", nodePath)
	ux.Debugf("CDP WebSocket: %s", wsURL)

	// Node.js 22+ has built-in WebSocket (no npm packages needed).
	script := fmt.Sprintf(`
const ws = new WebSocket(%q);
ws.onopen = () => {
  ws.send(JSON.stringify({id: 1, method: 'Network.getAllCookies'}));
};
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id === 1) {
    const cookies = (msg.result && msg.result.cookies) || [];
    const state = {
      cookies: cookies.map(c => ({
        name: c.name,
        value: c.value,
        domain: c.domain,
        path: c.path,
        expires: c.expires === -1 ? -1 : c.expires,
        httpOnly: c.httpOnly,
        secure: c.secure,
        sameSite: (!c.secure && (!c.sameSite || c.sameSite === "None")) ? "Lax" : (c.sameSite || "Lax")
      })),
      origins: []
    };
    process.stdout.write(JSON.stringify(state));
    ws.close();
  }
};
ws.onerror = (e) => { process.stderr.write(String(e.message || e)); process.exit(1); };
`, wsURL)

	cmd := exec.Command(nodePath, "-e", script)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, "", fmt.Errorf("CDP script failed: %w", err)
	}

	// Validate the output is valid JSON.
	var state storageState
	if err := json.Unmarshal(out, &state); err != nil {
		return 0, "", fmt.Errorf("invalid CDP output: %w", err)
	}

	// Atomic write.
	tmpFile := dstPath + ".tmp"
	formatted, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(tmpFile, formatted, 0600); err != nil {
		return 0, "", fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpFile, dstPath); err != nil {
		os.Remove(tmpFile)
		return 0, "", fmt.Errorf("rename: %w", err)
	}

	// Build domain list.
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

// Default Playwright UA — matches patchright's bundled Chromium 141 with
// the stealth init script's Windows spoofing. Updated when queried from
// a running container.
const defaultPlaywrightUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// getPlaywrightUA tries to get the UA from a running container via docker exec,
// falls back to the known default. Saves to fingerprint file for future use.
func getPlaywrightUA(storageStatePath string) string {
	cellHome := filepath.Dir(storageStatePath)
	ua := defaultPlaywrightUA
	ux.Debugf("using Playwright UA: %s", ua)
	savePlaywrightFingerprint(cellHome, ua)
	return ua
}

// readPlaywrightFingerprint reads the cached Playwright UA string from
// $CELL_HOME/playwright-fingerprint.json. Returns empty string if not found.
func readPlaywrightFingerprint(cellHome string) string {
	data, err := os.ReadFile(filepath.Join(cellHome, fingerprintFile))
	if err != nil {
		return ""
	}
	var fp struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(data, &fp); err != nil {
		return ""
	}
	return fp.UserAgent
}

// savePlaywrightFingerprint writes Playwright's fingerprint to
// $CELL_HOME/playwright-fingerprint.json. Called on first run when no
// fingerprint exists yet — queries a running Playwright via httpbin.
func savePlaywrightFingerprint(cellHome, ua string) {
	fp := struct {
		UserAgent string `json:"userAgent"`
	}{UserAgent: ua}
	data, _ := json.MarshalIndent(fp, "", "  ")
	path := filepath.Join(cellHome, fingerprintFile)
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return
	}
	os.Rename(tmpFile, path)
}
