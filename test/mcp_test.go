package container_test

// mcp_test.go — MCP server tests for all managed MCP servers.

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// e2eFormServer is a minimal Node.js HTTP server used by TestMcp_PlaywrightE2EFormSecrets.
// It serves an HTML form at GET / and writes submitted values to /tmp/form-output.txt.
// The listening port is written to /tmp/server-port.txt once the server is ready.
const e2eFormServer = `const http = require('http');
const fs = require('fs');

const server = http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/') {
    res.writeHead(200, {'Content-Type': 'text/html'});
    res.end('<!DOCTYPE html><html><body>' +
      '<form method="POST" action="/submit">' +
      '<input type="text" name="username" aria-label="username" id="username">' +
      '<input type="password" name="password" aria-label="password" id="password">' +
      '<button type="submit">Submit</button>' +
      '</form></body></html>');
  } else if (req.method === 'POST' && req.url === '/submit') {
    let body = '';
    req.on('data', d => { body += d; });
    req.on('end', () => {
      const p = new URLSearchParams(body);
      fs.writeFileSync('/tmp/form-output.txt',
        'username=' + (p.get('username') || '') + '\n' +
        'password=' + (p.get('password') || '') + '\n');
      res.writeHead(200, {'Content-Type': 'text/html'});
      res.end('<html><body>submitted</body></html>');
    });
  } else {
    res.writeHead(404); res.end();
  }
});

server.listen(0, '127.0.0.1', () => {
  const port = server.address().port;
  fs.writeFileSync('/tmp/server-port.txt', String(port));
  process.stdout.write('ready ' + port + '\n');
});
`

// e2eMcpClient drives patchright-mcp-cell via MCP stdio to fill the e2eFormServer form.
// Secret names (TEST_USERNAME, TEST_PASSWORD) are sent as field values; playwright-mcp
// resolves them via lookupSecret() using the secrets file written by patchright-mcp-cell.
// Prints "DONE" on success; exits non-zero on any failure.
const e2eMcpClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, re, time

CHROMIUM = '/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
USER_DATA = '/tmp/pw-e2e-test'

with open('/tmp/server-port.txt') as f:
    port = f.read().strip()
url = 'http://127.0.0.1:' + port
print('server url:', url, flush=True)

env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = USER_DATA

proc = subprocess.Popen(
    ['patchright-mcp-cell', '--headless', '--browser', 'chromium',
     '--executable-path', CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    env=env)

def send(msg):
    # @modelcontextprotocol/sdk v1.x uses newline-delimited JSON (one JSON object per line)
    proc.stdin.write((json.dumps(msg) + '\n').encode())
    proc.stdin.flush()

def recv():
    line = proc.stdout.readline()
    if not line:
        raise RuntimeError('EOF from playwright-mcp stdout')
    return json.loads(line)

try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{
        'protocolVersion':'2024-11-05','capabilities':{},
        'clientInfo':{'name':'e2e-test','version':'0'}}})
    r = recv()
    print('init:', r.get('result',{}).get('serverInfo',{}).get('name','?'), flush=True)

    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{
        'name':'browser_navigate','arguments':{'url':url}}})
    r = recv()
    print('navigate:', 'ok' if 'result' in r else 'ERROR:' + str(r.get('error')), flush=True)

    send({'jsonrpc':'2.0','id':3,'method':'tools/call','params':{
        'name':'browser_snapshot','arguments':{}}})
    r = recv()
    snapshot = r.get('result',{}).get('content',[{}])[0].get('text','')
    print('snapshot (200):', snapshot[:200], flush=True)

    username_ref = password_ref = submit_ref = None
    for line in snapshot.split('\n'):
        m = re.search(r'ref=(e\d+)', line)
        if not m:
            continue
        ref = m.group(1)
        lo = line.lower()
        if 'username' in lo and not username_ref:
            username_ref = ref
        elif 'password' in lo and not password_ref:
            password_ref = ref
        elif ('button' in lo or 'submit' in lo) and not submit_ref:
            submit_ref = ref
    print('refs: username=' + str(username_ref) + ' password=' + str(password_ref) + ' submit=' + str(submit_ref), flush=True)

    if not username_ref or not password_ref:
        print('ERROR: missing field refs; full snapshot:\n' + snapshot, file=sys.stderr)
        sys.exit(2)

    # browser_fill_form sends secret names as values; playwright-mcp resolves via lookupSecret()
    send({'jsonrpc':'2.0','id':4,'method':'tools/call','params':{
        'name':'browser_fill_form','arguments':{'fields':[
            {'name':'username','ref':username_ref,'type':'textbox','value':'TEST_USERNAME'},
            {'name':'password','ref':password_ref,'type':'textbox','value':'TEST_PASSWORD'},
        ]}}})
    r = recv()
    print('fill:', 'ok' if 'result' in r else 'ERROR:' + str(r.get('error')), flush=True)

    if submit_ref:
        send({'jsonrpc':'2.0','id':5,'method':'tools/call','params':{
            'name':'browser_click','arguments':{'ref':submit_ref,'element':'submit button'}}})
    else:
        # fallback: submit via JS if snapshot did not surface a button ref
        send({'jsonrpc':'2.0','id':5,'method':'tools/call','params':{
            'name':'browser_evaluate','arguments':{'function':"() => document.querySelector('form').submit()"}}})
    r = recv()
    print('submit:', 'ok' if 'result' in r else 'ERROR:' + str(r.get('error')), flush=True)

    time.sleep(1)  # let Node.js finish writing form-output.txt
    print('DONE', flush=True)
finally:
    proc.terminate()
    proc.wait()
`

// ── Playwright MCP ────────────────────────────────────────────────────────────

// TestMcp_PlaywrightSecretsFromDotEnv verifies patchright-mcp-cell reads key names from
// $USER_WORKING_DIR/.env and resolves values from the container environment.
// Only keys present in .env are forwarded — other container env vars are not exposed.
func TestMcp_PlaywrightSecretsFromDotEnv(t *testing.T) {
	c := startContainer(t, map[string]string{
		"APP_NAME":         "test",
		"HOST_USER":        hostUser,
		"TEST_PASSWORD":    "hello123",
		"GITHUB_TOKEN":     "ghtoken456",
		"USER_WORKING_DIR": "/tmp/test-wd",
	})

	_, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"})
	if code != 0 {
		t.Fatal("FAIL: patchright-mcp-cell not found on PATH")
	}

	// Create mock .env with the secret key names (values in .env are irrelevant;
	// wrapper looks them up from container env at runtime).
	_, code = exec(t, c, []string{"sh", "-c",
		"mkdir -p /tmp/test-wd && printf 'TEST_PASSWORD=placeholder\nGITHUB_TOKEN=placeholder\n' > /tmp/test-wd/.env"})
	if code != 0 {
		t.Fatal("FAIL: could not create test .env file")
	}

	out, code := exec(t, c, []string{"sh", "-c", `
		SECRETS_FILE=$(mktemp /tmp/pw-secrets-XXXXXX.env)
		trap 'rm -f "$SECRETS_FILE"' EXIT
		_ENV_FILE="${USER_WORKING_DIR:-}/.env"
		if [ -f "$_ENV_FILE" ]; then
			while IFS= read -r _line || [ -n "$_line" ]; do
				case "$_line" in '#'*|'') continue ;; esac
				_key="${_line%%=*}"
				_key="${_key#export }"
				[ -z "$_key" ] && continue
				if _val=$(printenv "$_key" 2>/dev/null); then
					printf '%s=%s\n' "$_key" "$_val"
				fi
			done < "$_ENV_FILE" >> "$SECRETS_FILE"
		fi
		cat "$SECRETS_FILE"
	`})
	if code != 0 {
		t.Fatalf("FAIL: wrapper logic failed (exit %d)", code)
	}
	// Resolved values from container env (not the placeholder from .env)
	if !strings.Contains(out, "TEST_PASSWORD=hello123") {
		t.Errorf("FAIL: expected TEST_PASSWORD=hello123, got:\n%s", out)
	}
	if !strings.Contains(out, "GITHUB_TOKEN=ghtoken456") {
		t.Errorf("FAIL: expected GITHUB_TOKEN=ghtoken456, got:\n%s", out)
	}
	// APP_NAME is a container env var but not in .env — must not be forwarded
	if strings.Contains(out, "APP_NAME=") {
		t.Errorf("FAIL: APP_NAME should not be in secrets (not in .env):\n%s", out)
	}
	t.Logf("PASS: secrets file:\n%s", out)
}

// TestMcp_PlaywrightE2EFormSecrets — full end-to-end secrets flow:
//  1. Container starts with TEST_USERNAME and TEST_PASSWORD env vars; .env lists those keys.
//  2. Assert playwright is registered in ~/.claude.json (merged by entrypoint).
//  3. Start a local Node.js HTTP server that serves a login form.
//  4. Drive patchright-mcp-cell via MCP stdio:
//     initialize → browser_navigate → browser_snapshot → browser_fill_form (secret names) → browser_click.
//  5. Assert /tmp/form-output.txt contains the real secret values (not the names).
func TestMcp_PlaywrightE2EFormSecrets(t *testing.T) {
	const (
		testUsername = "alice"
		testPassword = "s3cr3t123"
	)

	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "test",
		"TEST_USERNAME":    testUsername,
		"TEST_PASSWORD":    testPassword,
		"USER_WORKING_DIR": "/tmp/e2e-wd",
	})

	// Create .env with the secret key names so the wrapper knows which vars to forward.
	ctx := context.Background()
	if err := c.CopyToContainer(ctx,
		[]byte("TEST_USERNAME=\nTEST_PASSWORD=\n"),
		"/tmp/e2e-wd/.env", 0o644); err != nil {
		// CopyToContainer creates parent dirs automatically only when the file is in /tmp;
		// fall back to exec if copy fails.
		if _, code := exec(t, c, []string{"sh", "-c",
			"mkdir -p /tmp/e2e-wd && printf 'TEST_USERNAME=\nTEST_PASSWORD=\n' > /tmp/e2e-wd/.env"}); code != 0 {
			t.Fatalf("FAIL setup: could not create .env: %v", err)
		}
	}

	// Step 1: playwright registered in ~/.claude.json (merged by entrypoint.sh).
	raw, code := exec(t, c, []string{"cat", "/home/" + hostUser + "/.claude.json"})
	if code != 0 {
		t.Fatalf("FAIL step 1: ~/.claude.json not found (exit %d)", code)
	}
	var claudeCfg struct {
		McpServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &claudeCfg); err != nil {
		t.Fatalf("FAIL step 1: ~/.claude.json invalid JSON: %v", err)
	}
	entry, ok := claudeCfg.McpServers["playwright"]
	if !ok {
		t.Fatalf("FAIL step 1: playwright not in mcpServers; present keys: %v", claudeCfg.McpServers)
	}
	if !strings.HasSuffix(entry.Command, "patchright-mcp-cell") {
		t.Fatalf("FAIL step 1: playwright command=%q, want suffix patchright-mcp-cell", entry.Command)
	}
	t.Logf("PASS step 1: playwright registered, command=%s", entry.Command)

	// Step 2: copy Node.js form server and Python MCP client into the container.
	if err := c.CopyToContainer(ctx, []byte(e2eFormServer), "/tmp/form-server.js", 0o644); err != nil {
		t.Fatalf("FAIL step 2: copy form-server.js: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(e2eMcpClient), "/tmp/mcp-form-client.py", 0o755); err != nil {
		t.Fatalf("FAIL step 2: copy mcp-form-client.py: %v", err)
	}

	// Step 3: start Node.js server (backgrounds immediately, writes port to /tmp/server-port.txt).
	exec(t, c, []string{"bash", "-c", "node /tmp/form-server.js &"})
	_, portCode := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/server-port.txt ] && exit 0; sleep 0.5; done; exit 1"})
	if portCode != 0 {
		t.Fatal("FAIL step 3: Node.js server did not start (no port file in 5s)")
	}
	port, _ := exec(t, c, []string{"cat", "/tmp/server-port.txt"})
	t.Logf("step 3: Node.js form server on port %s", port)

	// Step 4: run Python MCP client — drives patchright-mcp-cell to fill form with secrets.
	mcpOut, mcpCode := exec(t, c, []string{"python3", "/tmp/mcp-form-client.py"})
	t.Logf("step 4 MCP client output:\n%s", mcpOut)
	if mcpCode != 0 {
		t.Fatalf("FAIL step 4: MCP client exited %d", mcpCode)
	}
	if !strings.Contains(mcpOut, "DONE") {
		t.Fatalf("FAIL step 4: MCP client did not complete:\n%s", mcpOut)
	}

	// Step 5: assert the real secret values reached the form (not the names).
	result, resultCode := exec(t, c, []string{"cat", "/tmp/form-output.txt"})
	if resultCode != 0 {
		t.Fatal("FAIL step 5: /tmp/form-output.txt not found — form was not submitted")
	}
	t.Logf("step 5 form output:\n%s", result)
	if !strings.Contains(result, "username="+testUsername) {
		t.Errorf("FAIL step 5: expected username=%s in:\n%s", testUsername, result)
	}
	if !strings.Contains(result, "password="+testPassword) {
		t.Errorf("FAIL step 5: expected password=%s (resolved secret) in:\n%s", testPassword, result)
	} else {
		t.Logf("PASS: form submitted with secrets resolved correctly")
	}
}

// ── Patchright stealth detection ──────────────────────────────────────────────

// e2eStealthDetector drives patchright-mcp-cell via MCP stdio to check browser fingerprints.
// Reads --init-script path from nix-mcp-servers.json (same args Claude Code uses in production).
// Navigates to the e2eFormServer and evaluates JS to check stealth spoofing.
// Outputs "STEALTH:{json}" with detection results.
const e2eStealthDetector = `#!/usr/bin/env python3
import subprocess, json, os, sys

CHROMIUM = '/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
USER_DATA = '/tmp/pw-stealth-test'

with open('/tmp/server-port.txt') as f:
    port = f.read().strip()

env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = USER_DATA

# patchright-mcp-cell wrapper auto-discovers --init-script and --config
# from ../share/patchright/ relative to itself. No need to pass explicitly.
proc = subprocess.Popen(
    ['patchright-mcp-cell', '--headless', '--browser', 'chromium',
     '--executable-path', CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env)

def send(msg):
    proc.stdin.write((json.dumps(msg) + '\n').encode())
    proc.stdin.flush()

def recv(timeout=60):
    import select
    if not select.select([proc.stdout], [], [], timeout)[0]:
        raise RuntimeError(f'recv timeout after {timeout}s')
    line = proc.stdout.readline()
    if not line: raise RuntimeError('EOF from patchright-mcp stdout')
    return json.loads(line)

try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{
        'protocolVersion':'2024-11-05','capabilities':{},
        'clientInfo':{'name':'stealth-test','version':'0'}}})
    r = recv()
    print('init:', r.get('result',{}).get('serverInfo',{}).get('name','?'), flush=True)

    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{
        'name':'browser_navigate','arguments':{'url':'http://127.0.0.1:'+port}}})
    r = recv()
    print('navigate:', 'ok' if 'result' in r else 'ERROR:'+str(r.get('error')), flush=True)

    # Evaluate stealth fingerprint checks in page context
    send({'jsonrpc':'2.0','id':3,'method':'tools/call','params':{
        'name':'browser_evaluate','arguments':{'function':"""() => {
            const r = {};
            r.webdriver = navigator.webdriver;
            r.hasChrome = typeof window.chrome !== 'undefined';
            r.hasChromeRuntime = !!(window.chrome && window.chrome.runtime);
            r.pluginsCount = navigator.plugins.length;
            r.languages = navigator.languages.join(',');
            try {
                const c = document.createElement('canvas');
                const gl = c.getContext('webgl');
                if (gl) {
                    r.webglVendor = gl.getParameter(37445);
                    r.webglRenderer = gl.getParameter(37446);
                } else { r.webglError = 'no context'; }
            } catch(e) { r.webglError = e.message; }
            return JSON.stringify(r);
        }"""}}})
    r = recv()
    text = r.get('result',{}).get('content',[{}])[0].get('text','')
    # browser_evaluate wraps result in markdown like: ### Result\n"{ ... }"\n### Ran...
    # Extract the JSON string between the first pair of double quotes on its own line
    import re
    m = re.search(r'^"(.*)"$', text, re.MULTILINE)
    if m:
        text = json.loads('"' + m.group(1) + '"')  # unescape \" etc
    print('STEALTH:' + text, flush=True)
finally:
    proc.terminate()
    proc.wait()
`

// TestMcp_PatchrightUndetected — verifies patchright + stealth init-script make the browser
// undetectable as automated. Launches patchright-mcp-cell with --init-script (same args
// Claude Code uses in production via nix-mcp-servers.json), navigates to a page, and asserts:
//   - navigator.webdriver is undefined (not true)
//   - window.chrome.runtime exists (mock)
//   - navigator.plugins.length >= 3 (mock)
//   - navigator.languages includes "en-US"
//   - WebGL vendor = "Intel Inc." (if available)
func TestMcp_PatchrightUndetected(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "test",
		"USER_WORKING_DIR": "/tmp/stealth-wd",
	})

	// Check binary and init-script are present
	_, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"})
	if code != 0 {
		t.Fatal("FAIL: patchright-mcp-cell not on PATH")
	}

	// Create empty .env (wrapper expects it)
	exec(t, c, []string{"sh", "-c", "mkdir -p /tmp/stealth-wd && touch /tmp/stealth-wd/.env"})

	// Start Node.js form server (reuse the e2eFormServer for a real page)
	if err := c.CopyToContainer(context.Background(),
		[]byte(e2eFormServer), "/tmp/stealth-form-server.js", 0o644); err != nil {
		t.Fatalf("FAIL: copy form server: %v", err)
	}
	exec(t, c, []string{"bash", "-c", "node /tmp/stealth-form-server.js &"})
	_, portCode := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/server-port.txt ] && exit 0; sleep 0.5; done; exit 1"})
	if portCode != 0 {
		t.Fatal("FAIL: form server did not start")
	}

	// Copy and run stealth detection client
	if err := c.CopyToContainer(context.Background(),
		[]byte(e2eStealthDetector), "/tmp/stealth-detect.py", 0o755); err != nil {
		t.Fatalf("FAIL: copy stealth detector: %v", err)
	}

	out, mcpCode := exec(t, c, []string{"python3", "/tmp/stealth-detect.py"})
	t.Logf("stealth detector output:\n%s", out)
	if mcpCode != 0 {
		t.Fatalf("FAIL: stealth detector exited %d", mcpCode)
	}

	// Parse STEALTH:{json} line from output
	var stealthJSON string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "STEALTH:") {
			stealthJSON = strings.TrimPrefix(line, "STEALTH:")
			break
		}
	}
	if stealthJSON == "" {
		t.Fatal("FAIL: no STEALTH: line in output")
	}

	var result struct {
		Webdriver     interface{} `json:"webdriver"`
		HasChrome     bool        `json:"hasChrome"`
		HasChromeRT   bool        `json:"hasChromeRuntime"`
		PluginsCount  int         `json:"pluginsCount"`
		Languages     string      `json:"languages"`
		WebGLVendor   string      `json:"webglVendor"`
		WebGLRenderer string      `json:"webglRenderer"`
		WebGLError    string      `json:"webglError"`
	}
	if err := json.Unmarshal([]byte(stealthJSON), &result); err != nil {
		t.Fatalf("FAIL: parse stealth JSON: %v\nraw: %s", err, stealthJSON)
	}

	// navigator.webdriver must be undefined (null in JSON), not true
	if result.Webdriver == true {
		t.Errorf("FAIL: navigator.webdriver = true (detected as bot)")
	} else {
		t.Logf("PASS: navigator.webdriver = %v", result.Webdriver)
	}

	// window.chrome + chrome.runtime must exist (init-script mock)
	if !result.HasChrome {
		t.Errorf("FAIL: window.chrome missing")
	}
	if !result.HasChromeRT {
		t.Errorf("FAIL: window.chrome.runtime missing")
	} else {
		t.Logf("PASS: chrome.runtime present")
	}

	// navigator.plugins must have entries (init-script mock)
	if result.PluginsCount < 3 {
		t.Errorf("FAIL: navigator.plugins.length = %d, want >= 3", result.PluginsCount)
	} else {
		t.Logf("PASS: navigator.plugins.length = %d", result.PluginsCount)
	}

	// navigator.languages must include en-US
	if !strings.Contains(result.Languages, "en-US") {
		t.Errorf("FAIL: languages = %q, want en-US", result.Languages)
	} else {
		t.Logf("PASS: languages = %s", result.Languages)
	}

	// WebGL renderer spoof (may not be available in headless)
	if result.WebGLError != "" {
		t.Logf("SKIP: WebGL not available (%s)", result.WebGLError)
	} else {
		if result.WebGLVendor != "Intel Inc." {
			t.Errorf("FAIL: WebGL vendor = %q, want 'Intel Inc.'", result.WebGLVendor)
		} else {
			t.Logf("PASS: WebGL vendor = %s", result.WebGLVendor)
		}
		if result.WebGLRenderer != "Intel Iris OpenGL Engine" {
			t.Errorf("FAIL: WebGL renderer = %q, want 'Intel Iris OpenGL Engine'", result.WebGLRenderer)
		} else {
			t.Logf("PASS: WebGL renderer = %s", result.WebGLRenderer)
		}
	}
}

// ── Detection Suite ──────────────────────────────────────────────────────────
//
// Self-hosted bot detection suite derived from community tools:
//   - BotD      (github.com/fingerprintjs/BotD)       — automation framework detection
//   - fpscanner (github.com/antoinevastel/fpscanner)   — headless/selenium artifact checks
//   - CreepJS   (github.com/AbrahamJuliot/creepjs)     — fingerprint consistency checks
//   - headless-detector (andriyshevchenko)              — modern headless detection vectors
//
// Architecture: Go test → Python MCP client → patchright-mcp-cell → self-hosted HTML page.
// The HTML page runs all detection checks client-side, stores results in window.__DETECTION.
// The Python client reads the results via browser_evaluate and outputs DETECTION:{json}.
// Go subtests assert each detection category independently.

// e2eDetectionPage is a self-hosted bot detection page. It runs all checks client-side
// and stores structured results in window.__DETECTION. Title changes to "DONE" when complete.
const e2eDetectionPage = `<!DOCTYPE html>
<html><head><title>Detection Suite</title></head>
<body><pre id="status">running...</pre>
<script>
(async () => {
  const r = {};

  // ── BotD: WebDriver detection ──────────────────────────────────────────
  r.webdriver = navigator.webdriver;

  // ── BotD: Chrome object ────────────────────────────────────────────────
  r.hasChrome = typeof window.chrome !== 'undefined';
  r.hasChromeRuntime = !!(window.chrome && window.chrome.runtime);

  // ── fpscanner: Plugins ─────────────────────────────────────────────────
  r.pluginsCount = navigator.plugins.length;
  r.pluginsIsPluginArray = navigator.plugins instanceof PluginArray;

  // ── Languages ──────────────────────────────────────────────────────────
  r.languages = Array.from(navigator.languages);

  // ── WebGL (CreepJS / headless-detector) ────────────────────────────────
  try {
    const canvas = document.createElement('canvas');
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');
    if (gl) {
      const ext = gl.getExtension('WEBGL_debug_renderer_info');
      r.webglAvailable = true;
      r.webglVendor = ext ? gl.getParameter(ext.UNMASKED_VENDOR_WEBGL) : '';
      r.webglRenderer = ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : '';
    } else {
      r.webglAvailable = false;
    }
  } catch(e) {
    r.webglAvailable = false;
    r.webglError = e.message;
  }

  // ── Canvas 2D fingerprint (CreepJS) ────────────────────────────────────
  try {
    const c = document.createElement('canvas');
    c.width = 200; c.height = 50;
    const ctx = c.getContext('2d');
    ctx.textBaseline = 'top';
    ctx.font = '14px Arial';
    ctx.fillStyle = '#f60';
    ctx.fillRect(125, 1, 62, 20);
    ctx.fillStyle = '#069';
    ctx.fillText('devcell-detect', 2, 15);
    ctx.strokeStyle = 'rgba(102, 204, 0, 0.7)';
    ctx.arc(50, 25, 20, 0, Math.PI * 2, true);
    ctx.stroke();
    const dataURL = c.toDataURL();
    r.canvasLength = dataURL.length;
    r.canvasAvailable = true;
  } catch(e) {
    r.canvasAvailable = false;
    r.canvasError = e.message;
  }

  // ── Audio fingerprint (CreepJS) ────────────────────────────────────────
  try {
    const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    if (audioCtx.state === 'suspended') {
      await Promise.race([
        audioCtx.resume(),
        new Promise((_, reject) => setTimeout(() => reject(new Error('AudioContext resume timeout')), 3000))
      ]);
    }
    const oscillator = audioCtx.createOscillator();
    const analyser = audioCtx.createAnalyser();
    const compressor = audioCtx.createDynamicsCompressor();
    const gain = audioCtx.createGain();
    oscillator.type = 'triangle';
    oscillator.frequency.setValueAtTime(10000, audioCtx.currentTime);
    compressor.threshold.setValueAtTime(-50, audioCtx.currentTime);
    compressor.knee.setValueAtTime(40, audioCtx.currentTime);
    compressor.ratio.setValueAtTime(12, audioCtx.currentTime);
    compressor.attack.setValueAtTime(0, audioCtx.currentTime);
    compressor.release.setValueAtTime(0.25, audioCtx.currentTime);
    oscillator.connect(compressor);
    compressor.connect(analyser);
    gain.gain.value = 0;
    analyser.connect(gain);
    gain.connect(audioCtx.destination);
    oscillator.start(0);
    await new Promise(resolve => setTimeout(resolve, 200));
    const freqData = new Float32Array(analyser.frequencyBinCount);
    analyser.getFloatFrequencyData(freqData);
    let sum = 0;
    for (let i = 0; i < freqData.length; i++) sum += Math.abs(freqData[i]);
    r.audioSum = sum;
    r.audioAvailable = sum > 0;
    oscillator.stop();
    audioCtx.close();
  } catch(e) {
    r.audioAvailable = false;
    r.audioError = e.message;
  }

  // ── Screen / viewport (headless-detector) ──────────────────────────────
  r.screenWidth = screen.width;
  r.screenHeight = screen.height;
  r.colorDepth = screen.colorDepth;
  r.devicePixelRatio = window.devicePixelRatio;
  r.outerWidth = window.outerWidth;
  r.outerHeight = window.outerHeight;

  // ── Codecs (fpscanner) ─────────────────────────────────────────────────
  const v = document.createElement('video');
  r.codecH264 = v.canPlayType('video/mp4; codecs="avc1.42E01E"');
  r.codecWebm = v.canPlayType('video/webm; codecs="vp8"');
  r.codecOgg = v.canPlayType('audio/ogg; codecs="vorbis"');

  // ── Permissions API (BotD) ─────────────────────────────────────────────
  try {
    const perm = await navigator.permissions.query({name: 'notifications'});
    r.permissionsWorks = true;
    r.notificationState = perm.state;
  } catch(e) {
    r.permissionsWorks = false;
  }

  // ── navigator.connection (headless-detector) ───────────────────────────
  r.hasConnection = !!navigator.connection;
  if (navigator.connection) {
    r.effectiveType = navigator.connection.effectiveType;
  }

  // ── Selenium artifacts (fpscanner) ─────────────────────────────────────
  const seleniumKeys = [
    'webdriver', '__driver_evaluate', '__webdriver_evaluate',
    '__selenium_evaluate', '__fxdriver_evaluate', '__driver_unwrapped',
    '__webdriver_unwrapped', '__selenium_unwrapped', '__fxdriver_unwrapped',
    '_Selenium_IDE_Recorder', '_selenium', 'calledSelenium',
    '_WEBDRIVER_ELEM_CACHE', 'ChromeDriverw'
  ];
  r.seleniumArtifacts = seleniumKeys.filter(k => k in document || k in window);

  // ── CDP artifacts (BotD) ───────────────────────────────────────────────
  r.hasCDCArray = !!window.cdc_adoQpoasnfa76pfcZLmcfl_Array;
  r.hasCDCPromise = !!window.cdc_adoQpoasnfa76pfcZLmcfl_Promise;
  r.hasCDCSymbol = !!window.cdc_adoQpoasnfa76pfcZLmcfl_Symbol;

  // ── UA consistency ─────────────────────────────────────────────────────
  r.userAgent = navigator.userAgent;
  r.platform = navigator.platform;
  if (navigator.userAgentData) {
    try {
      const hea = await navigator.userAgentData.getHighEntropyValues(
        ['platform', 'architecture', 'platformVersion', 'fullVersionList']
      );
      r.uaData = {
        platform: hea.platform,
        architecture: hea.architecture,
        platformVersion: hea.platformVersion
      };
    } catch(e) {
      r.uaDataError = e.message;
    }
  }

  // ── Font enumeration (CreepJS) ─────────────────────────────────────────
  // Check if common system fonts are available via canvas measureText trick.
  const testFonts = [
    // Common system fonts (some Windows-only, tests cross-platform coverage)
    'Arial', 'Courier New', 'Georgia', 'Times New Roman', 'Verdana',
    'Trebuchet MS', 'Helvetica', 'Roboto', 'Open Sans', 'Lato',
    // Installed via nix (developer + UI fonts)
    'Fira Code', 'DejaVu Sans', 'Liberation Mono', 'Noto Sans',
    'Noto Serif', 'Inter', 'IBM Plex Sans', 'IBM Plex Mono',
    'Source Sans 3', 'Source Serif 4', 'Montserrat', 'Raleway',
    'Work Sans', 'Cabin', 'Cantarell', 'Gentium', 'Comic Neue',
    'Zilla Slab', 'Fira Sans', 'PT Sans', 'Atkinson Hyperlegible',
    'Quicksand', 'Poppins', 'Rubik', 'Karla', 'Barlow', 'Lexend',
    'Iosevka', 'Cascadia Code', 'Ubuntu Sans', 'DejaVu Serif',
    'Liberation Sans', 'Liberation Serif', 'Noto Sans Mono',
    'DejaVu Sans Mono', 'Noto Color Emoji'
  ];
  const baseFonts = ['monospace', 'sans-serif', 'serif'];
  const testStr = 'mmmmmmmmmmlli';
  const testSize = '72px';
  const detectedFonts = [];
  const fc = document.createElement('canvas');
  const fctx = fc.getContext('2d');
  const baseWidths = {};
  for (const base of baseFonts) {
    fctx.font = testSize + ' ' + base;
    baseWidths[base] = fctx.measureText(testStr).width;
  }
  for (const font of testFonts) {
    let detected = false;
    for (const base of baseFonts) {
      fctx.font = testSize + ' "' + font + '", ' + base;
      if (fctx.measureText(testStr).width !== baseWidths[base]) {
        detected = true;
        break;
      }
    }
    if (detected) detectedFonts.push(font);
  }
  r.detectedFonts = detectedFonts;
  r.fontCount = detectedFonts.length;

  window.__DETECTION = r;
  document.getElementById('status').textContent = JSON.stringify(r, null, 2);
  document.title = 'DONE';
})();
</script>
</body></html>`

// e2eDetectionClient drives patchright-mcp-cell via MCP stdio, navigates to the
// self-hosted detection page, waits for completion, and reads window.__DETECTION.
// Outputs "DETECTION:{json}" for the Go test to parse.
const e2eDetectionClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, threading, http.server, time, re

# ── Serve detection page ──────────────────────────────────────────────────────
DETECTION_HTML = open('/tmp/detection-page.html').read()

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/html')
        self.end_headers()
        self.wfile.write(DETECTION_HTML.encode())
    def log_message(self, *a): pass

srv = http.server.HTTPServer(('127.0.0.1', 0), Handler)
port = srv.server_address[1]
threading.Thread(target=srv.serve_forever, daemon=True).start()
print(f'detection-server: port={port}', flush=True)

# ── Start patchright-mcp-cell via MCP stdio ───────────────────────────────────
# patchright-mcp-cell wrapper auto-discovers --init-script and --config
# from ../share/patchright/ relative to itself. No need to pass explicitly.
env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = '/tmp/pw-detect-suite'

cmd = ['patchright-mcp-cell', '--browser', 'chromium']

proc = subprocess.Popen(
    cmd,
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env)

msg_id = 0
def send(method, params=None):
    global msg_id
    msg_id += 1
    msg = {'jsonrpc':'2.0','id':msg_id,'method':method}
    if params: msg['params'] = params
    proc.stdin.write((json.dumps(msg) + '\n').encode())
    proc.stdin.flush()
    return msg_id

def recv(timeout=60):
    import select
    if not select.select([proc.stdout], [], [], timeout)[0]:
        raise RuntimeError(f'recv timeout after {timeout}s')
    line = proc.stdout.readline()
    if not line: raise RuntimeError('EOF from patchright-mcp stdout')
    return json.loads(line)

try:
    # Initialize
    send('initialize', {
        'protocolVersion':'2024-11-05','capabilities':{},
        'clientInfo':{'name':'detection-suite','version':'0'}})
    r = recv()
    print('mcp-init: ' + r.get('result',{}).get('serverInfo',{}).get('name','?'), flush=True)

    # Navigate to detection page
    send('tools/call', {'name':'browser_navigate',
        'arguments':{'url': f'http://127.0.0.1:{port}'}})
    r = recv()
    if 'error' in r:
        print(f'ERROR: navigate failed: {r["error"]}', file=sys.stderr)
        sys.exit(3)
    print('navigate: ok', flush=True)

    # Wait for detection to complete (title changes to DONE)
    for attempt in range(30):
        time.sleep(0.5)
        send('tools/call', {'name':'browser_evaluate',
            'arguments':{'function':'() => document.title'}})
        r = recv()
        text = r.get('result',{}).get('content',[{}])[0].get('text','')
        if 'DONE' in text:
            print('detection: complete', flush=True)
            break
    else:
        print('ERROR: detection page did not complete in 15s', file=sys.stderr)
        sys.exit(4)

    # Read detection results
    send('tools/call', {'name':'browser_evaluate',
        'arguments':{'function':'() => JSON.stringify(window.__DETECTION)'}})
    r = recv()
    text = r.get('result',{}).get('content',[{}])[0].get('text','')
    # browser_evaluate wraps result in markdown; extract JSON from quoted line
    m = re.search(r'^"(.*)"$', text, re.MULTILINE)
    if m:
        text = json.loads('"' + m.group(1) + '"')
    print('DETECTION:' + text, flush=True)

finally:
    proc.terminate()
    proc.wait()
    srv.shutdown()
`

// TestMcp_PatchrightDetectionSuite — comprehensive bot detection suite using a self-hosted
// detection page derived from BotD, fpscanner, CreepJS, and headless-detector.
// Each detection category is a subtest for independent pass/fail tracking.
//
// RED tests (expected to fail until Mesa WebGL + fonts are configured):
//   - Fingerprint/WebGLAvailable — no WebGL context in headless without software GL flags
//   - Fingerprint/WebGLVendorSpoofed — depends on WebGL being available
//   - Fingerprint/WebGLRendererSpoofed — depends on WebGL being available
//   - Environment/FontCount — too few fonts installed
func TestMcp_PatchrightDetectionSuite(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "test",
		"USER_WORKING_DIR": "/tmp/detect-wd",
	})

	// Verify patchright-mcp-cell is present
	_, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"})
	if code != 0 {
		t.Fatal("FAIL: patchright-mcp-cell not on PATH")
	}

	// Start PulseAudio with null sink — Chromium needs an audio backend
	// to produce real AudioContext frequency data (otherwise all-zero = bot signal).
	// Uses -n -F to skip default config (no dbus dependency) and explicitly loads
	// native-protocol-unix (socket) + null-sink (virtual audio output).
	exec(t, c, []string{"gosu", hostUser, "mkdir", "-p", "/tmp/pulse-runtime/pulse"})
	exec(t, c, []string{"gosu", hostUser, "sh", "-c",
		"dbus-uuidgen > /etc/machine-id 2>/dev/null || true"})
	_, paCode := exec(t, c, []string{"gosu", hostUser, "sh", "-c",
		`XDG_RUNTIME_DIR=/tmp/pulse-runtime pulseaudio --daemonize=yes --exit-idle-time=-1 ` +
			`--disable-shm=true -n ` +
			`--load="module-null-sink sink_name=NullSink" ` +
			`--load="module-native-protocol-unix"`,
	})
	if paCode != 0 {
		t.Log("WARNING: PulseAudio not available — AudioContext test may fail")
	} else {
		t.Log("PulseAudio started with null sink")
	}

	// Create empty .env (wrapper expects it)
	exec(t, c, []string{"sh", "-c", "mkdir -p /tmp/detect-wd && touch /tmp/detect-wd/.env"})

	// Copy detection page and client into container
	ctx := context.Background()
	if err := c.CopyToContainer(ctx,
		[]byte(e2eDetectionPage), "/tmp/detection-page.html", 0o644); err != nil {
		t.Fatalf("FAIL: copy detection page: %v", err)
	}
	if err := c.CopyToContainer(ctx,
		[]byte(e2eDetectionClient), "/tmp/detection-client.py", 0o755); err != nil {
		t.Fatalf("FAIL: copy detection client: %v", err)
	}

	// Run detection client — set PULSE_SERVER so Chromium finds PulseAudio
	out, exitCode := exec(t, c, []string{"sh", "-c",
		"PULSE_SERVER=unix:/tmp/pulse-runtime/pulse/native " +
			"XDG_RUNTIME_DIR=/tmp/pulse-runtime " +
			"python3 /tmp/detection-client.py"})
	t.Logf("detection client output:\n%s", out)
	if exitCode != 0 {
		t.Fatalf("FAIL: detection client exited %d", exitCode)
	}

	// Parse DETECTION:{json} line
	var detectionJSON string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "DETECTION:") {
			detectionJSON = strings.TrimPrefix(line, "DETECTION:")
			break
		}
	}
	if detectionJSON == "" {
		t.Fatal("FAIL: no DETECTION: line in output")
	}

	var d struct {
		// Stealth (init-script)
		Webdriver      interface{} `json:"webdriver"`
		HasChrome      bool        `json:"hasChrome"`
		HasChromeRT    bool        `json:"hasChromeRuntime"`
		PluginsCount   int         `json:"pluginsCount"`
		PluginsIsArray bool        `json:"pluginsIsPluginArray"`
		Languages      []string    `json:"languages"`
		// WebGL
		WebGLAvailable bool   `json:"webglAvailable"`
		WebGLVendor    string `json:"webglVendor"`
		WebGLRenderer  string `json:"webglRenderer"`
		WebGLError     string `json:"webglError"`
		// Canvas
		CanvasAvailable bool   `json:"canvasAvailable"`
		CanvasLength    int    `json:"canvasLength"`
		CanvasError     string `json:"canvasError"`
		// Audio
		AudioAvailable bool    `json:"audioAvailable"`
		AudioSum       float64 `json:"audioSum"`
		AudioError     string  `json:"audioError"`
		// Screen
		ScreenWidth      int     `json:"screenWidth"`
		ScreenHeight     int     `json:"screenHeight"`
		ColorDepth       int     `json:"colorDepth"`
		DevicePixelRatio float64 `json:"devicePixelRatio"`
		OuterWidth       int     `json:"outerWidth"`
		OuterHeight      int     `json:"outerHeight"`
		// Codecs
		CodecH264 string `json:"codecH264"`
		CodecWebm string `json:"codecWebm"`
		CodecOgg  string `json:"codecOgg"`
		// Permissions
		PermissionsWorks  bool   `json:"permissionsWorks"`
		NotificationState string `json:"notificationState"`
		// Connection
		HasConnection bool   `json:"hasConnection"`
		EffectiveType string `json:"effectiveType"`
		// Artifacts
		SeleniumArtifacts []string `json:"seleniumArtifacts"`
		HasCDCArray       bool     `json:"hasCDCArray"`
		HasCDCPromise     bool     `json:"hasCDCPromise"`
		HasCDCSymbol      bool     `json:"hasCDCSymbol"`
		// UA
		UserAgent string `json:"userAgent"`
		Platform  string `json:"platform"`
		UAData    *struct {
			Platform     string `json:"platform"`
			Architecture string `json:"architecture"`
		} `json:"uaData"`
		UADataError string `json:"uaDataError"`
		// Fonts
		DetectedFonts []string `json:"detectedFonts"`
		FontCount     int      `json:"fontCount"`
	}
	if err := json.Unmarshal([]byte(detectionJSON), &d); err != nil {
		t.Fatalf("FAIL: parse detection JSON: %v\nraw: %s", err, detectionJSON)
	}
	t.Logf("Detection results: %s", detectionJSON)

	// ── Stealth subtests (should be GREEN with current init-script) ───────

	t.Run("Stealth/WebDriverUndefined", func(t *testing.T) {
		if d.Webdriver == true {
			t.Errorf("navigator.webdriver = true (detected as bot)")
		}
	})

	t.Run("Stealth/ChromeObject", func(t *testing.T) {
		if !d.HasChrome {
			t.Errorf("window.chrome missing")
		}
		if !d.HasChromeRT {
			t.Errorf("window.chrome.runtime missing")
		}
	})

	t.Run("Stealth/Plugins", func(t *testing.T) {
		if d.PluginsCount < 3 {
			t.Errorf("plugins.length = %d, want >= 3", d.PluginsCount)
		}
		if !d.PluginsIsArray {
			t.Errorf("plugins not instanceof PluginArray")
		}
	})

	t.Run("Stealth/Languages", func(t *testing.T) {
		found := false
		for _, l := range d.Languages {
			if l == "en-US" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("languages = %v, want en-US", d.Languages)
		}
	})

	t.Run("Stealth/NoSeleniumArtifacts", func(t *testing.T) {
		if len(d.SeleniumArtifacts) > 0 {
			t.Errorf("selenium artifacts found: %v", d.SeleniumArtifacts)
		}
	})

	t.Run("Stealth/NoCDPArtifacts", func(t *testing.T) {
		if d.HasCDCArray || d.HasCDCPromise || d.HasCDCSymbol {
			t.Errorf("CDP artifacts found: array=%v promise=%v symbol=%v",
				d.HasCDCArray, d.HasCDCPromise, d.HasCDCSymbol)
		}
	})

	// ── Fingerprint subtests (RED until Mesa WebGL configured) ────────────

	t.Run("Fingerprint/WebGLAvailable", func(t *testing.T) {
		if !d.WebGLAvailable {
			t.Errorf("WebGL context not available (error: %s) — need software GL flags", d.WebGLError)
		}
	})

	t.Run("Fingerprint/WebGLVendorSpoofed", func(t *testing.T) {
		if !d.WebGLAvailable {
			t.Errorf("WebGL not available, cannot verify vendor spoof")
			return
		}
		if d.WebGLVendor != "Intel Inc." {
			t.Errorf("WebGL vendor = %q, want 'Intel Inc.'", d.WebGLVendor)
		}
	})

	t.Run("Fingerprint/WebGLRendererSpoofed", func(t *testing.T) {
		if !d.WebGLAvailable {
			t.Errorf("WebGL not available, cannot verify renderer spoof")
			return
		}
		if d.WebGLRenderer != "Intel Iris OpenGL Engine" {
			t.Errorf("WebGL renderer = %q, want 'Intel Iris OpenGL Engine'", d.WebGLRenderer)
		}
	})

	t.Run("Fingerprint/Canvas2D", func(t *testing.T) {
		if !d.CanvasAvailable {
			t.Errorf("canvas 2D not available: %s", d.CanvasError)
			return
		}
		// A real canvas fingerprint produces a data URL > 1000 chars
		if d.CanvasLength < 1000 {
			t.Errorf("canvas dataURL length = %d, want > 1000 (blank canvas?)", d.CanvasLength)
		}
	})

	t.Run("Fingerprint/AudioContext", func(t *testing.T) {
		if !d.AudioAvailable {
			t.Errorf("audio fingerprint not available: %s", d.AudioError)
			return
		}
		if d.AudioSum == 0 {
			t.Errorf("audio frequency sum = 0 (no audio data)")
		}
	})

	// ── Environment subtests ─────────────────────────────────────────────

	t.Run("Environment/Screen", func(t *testing.T) {
		if d.ColorDepth < 24 {
			t.Errorf("colorDepth = %d, want >= 24", d.ColorDepth)
		}
		if d.DevicePixelRatio == 0 {
			t.Errorf("devicePixelRatio = 0")
		}
	})

	t.Run("Environment/CodecWebm", func(t *testing.T) {
		if d.CodecWebm == "" {
			t.Errorf("WebM codec not supported")
		}
	})

	t.Run("Environment/CodecOgg", func(t *testing.T) {
		if d.CodecOgg == "" {
			t.Errorf("Ogg codec not supported")
		}
	})

	t.Run("Environment/Permissions", func(t *testing.T) {
		if !d.PermissionsWorks {
			t.Errorf("Permissions API query failed")
		}
	})

	t.Run("Environment/FontCount", func(t *testing.T) {
		// Real browsers typically detect 10+ fonts from the test set.
		// Headless with minimal fonts detects < 5.
		t.Logf("detected fonts (%d): %v", d.FontCount, d.DetectedFonts)
		if d.FontCount < 23 {
			t.Errorf("fontCount = %d, want >= 23 (too few fonts installed)", d.FontCount)
		}
	})
}

// TestMcp_ClaudeJsonBackupOnMerge — when ~/.claude.json pre-exists and backupBeforeMerge=true,
// a timestamped backup must be created before the merge overwrites the file.
func TestMcp_ClaudeJsonBackupOnMerge(t *testing.T) {
	c := startEnvContainer(t)

	// Confirm the staging file declares backupBeforeMerge=true.
	raw, code := exec(t, c, []string{"cat", "/etc/claude-code/nix-mcp-servers.json"})
	if code != 0 {
		t.Fatalf("FAIL: could not read nix-mcp-servers.json (exit %d)", code)
	}
	var staging struct {
		BackupBeforeMerge bool `json:"backupBeforeMerge"`
	}
	staging.BackupBeforeMerge = true // default if key absent
	if err := json.Unmarshal([]byte(raw), &staging); err != nil {
		t.Fatalf("FAIL: invalid JSON in nix-mcp-servers.json: %v", err)
	}
	if !staging.BackupBeforeMerge {
		t.Skip("backupBeforeMerge=false in staging file — backup test not applicable")
	}
	t.Logf("backupBeforeMerge=true confirmed in nix-mcp-servers.json")

	// Run the backup+merge logic against a temp file that already has content,
	// simulating a second container start where ~/.claude.json pre-exists.
	// Uses the real /etc/claude-code/nix-mcp-servers.json staging file.
	out, code := exec(t, c, []string{"bash", "-c", `
		NM=/etc/claude-code/nix-mcp-servers.json
		TARGET=$(mktemp /tmp/test-claude-XXXXXX.json)
		echo '{"mcpServers":{"pre-existing":{"type":"stdio","command":"old-tool","args":[],"env":{}}}}' > "$TARGET"

		BACKUP_BEFORE=$(jq -r '.backupBeforeMerge // true' "$NM")
		BACKUP_FILE=""
		if [ "$BACKUP_BEFORE" = "true" ]; then
			BACKUP_FILE="${TARGET}.backup-$(date +%Y%m%d-%H%M%S)"
			cp "$TARGET" "$BACKUP_FILE"
		fi

		TEMP=$(mktemp)
		jq -s '.[0] as $e | .[1].mcpServers as $n | $e | .mcpServers = (($e.mcpServers // {}) + ($n // {}))' \
			"$TARGET" "$NM" > "$TEMP" && mv "$TEMP" "$TARGET"

		if [ -n "$BACKUP_FILE" ] && [ -f "$BACKUP_FILE" ]; then
			BACKUP_HAS_PRE=$(jq -e '.mcpServers["pre-existing"]' "$BACKUP_FILE" >/dev/null 2>&1 && echo yes || echo no)
			echo "backup_created:yes"
			echo "backup_has_pre_existing:$BACKUP_HAS_PRE"
		else
			echo "backup_created:no"
		fi
		MERGED_COUNT=$(jq '.mcpServers | length' "$TARGET")
		echo "merged_count:$MERGED_COUNT"

		rm -f "$TARGET" "$BACKUP_FILE"
	`})
	if code != 0 {
		t.Fatalf("FAIL: backup/merge script failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "backup_created:yes") {
		t.Errorf("FAIL: no backup created despite backupBeforeMerge=true\n%s", out)
	}
	if !strings.Contains(out, "backup_has_pre_existing:yes") {
		t.Errorf("FAIL: backup file does not contain the pre-existing entry\n%s", out)
	}
	// merged result must have both pre-existing + nix servers
	if m := regexp.MustCompile(`merged_count:(\d+)`).FindStringSubmatch(out); m != nil {
		if n, _ := strconv.Atoi(m[1]); n <= 1 {
			t.Errorf("FAIL: merged file should have >1 server (pre-existing + nix), got %d\n%s", n, out)
		}
	} else {
		t.Errorf("FAIL: merged_count not found in output\n%s", out)
	}
	t.Logf("PASS: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
}
