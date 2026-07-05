package container_test

// stealth_l2_test.go — CELL-153: L2 runtime stealth verification.
//
// Drives `patchright-mcp-cell` via MCP stdio (the EXACT path Claude Code uses
// in production), navigates the launched Chromium to an in-container probe
// server, and asserts on 4 layers of stealth:
//
//   Layer 0 — sentinel: window.__cellStealth defined (CELL-161 silent-abort)
//   Layer 1 — CDP HTTP headers: Sec-CH-UA-Arch / Sec-CH-UA-Platform (CELL-68)
//   Layer 2 — main-thread JS: navigator.platform, webdriver, chrome.runtime,
//             getHighEntropyValues().architecture (CELL-169, CELL-68)
//   Layer 3 — Worker JS: navigator.platform + architecture (CELL-34, CELL-25)
//
// Existing stealth tests in stealth_test.go are L1 (regex over nix source);
// TestMcp_PatchrightUndetected in mcp_test.go is now a smoke check. Neither
// catches the CELL-161 class of bugs where the init script silently aborts
// at parse time and every spoof becomes dead code.
//
// Server is Python (ThreadingHTTPServer) not Node — thin variant does not
// have node on PATH but python3 is always present.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stealthProbeServer — Python HTTP server with three endpoints:
//
//	GET  /probe      → HTML page; runs in-page JS, spawns a Worker fetched
//	                   from /worker.js, POSTs collected signals to /results.
//	GET  /worker.js  → worker source.
//	POST /results    → accepts the probe's JSON output and writes it to
//	                   /tmp/probe-results.json.
//
// Every incoming request's headers are appended to /tmp/headers.json (one
// JSON object per line) so the test can verify CDP-emitted Sec-CH-UA-*
// headers. The probe page advertises Accept-CH + Critical-CH so Chrome
// retries the navigation with all advertised high-entropy hints attached.
const stealthProbeServer = `#!/usr/bin/env python3
import json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

WORKER_JS = (
    "self.onmessage = async () => {\n"
    "  const r = { platform: navigator.platform, ua: navigator.userAgent };\n"
    "  r.cellStealth = typeof self.__cellStealth;\n"
    "  if (typeof navigator.userAgentData !== 'undefined') {\n"
    "    try {\n"
    "      const h = await navigator.userAgentData.getHighEntropyValues(['architecture','platform','bitness']);\n"
    "      r.hea_arch = h.architecture;\n"
    "      r.hea_platform = h.platform;\n"
    "      r.hea_bitness = h.bitness;\n"
    "    } catch (e) { r.hea_error = String(e); }\n"
    "  } else {\n"
    "    r.hea_error = 'navigator.userAgentData undefined in worker';\n"
    "  }\n"
    "  self.postMessage(r);\n"
    "};\n"
)

# HTML uses single-quoted JS strings + concatenation so the Python source
# (triple-quoted) can hold it without escape acrobatics. The page collects
# main-thread + Worker signals and POSTs them to /results.
HTML = (
    '<!DOCTYPE html><html><head><title>stealth-l2 probe</title></head><body>'
    '<pre id="result">pending</pre>'
    '<script>'
    '(async function(){'
    '  const out = { main: {}, worker: {} };'
    '  out.main.cellStealth = typeof window.__cellStealth;'
    '  out.main.cellStealthValue = (typeof window.__cellStealth !== "undefined") ? JSON.stringify(window.__cellStealth) : null;'
    '  out.main.platform = navigator.platform;'
    '  out.main.webdriverType = typeof navigator.webdriver;'
    '  out.main.webdriver = navigator.webdriver;'
    '  out.main.hasChrome = typeof window.chrome === "object" && window.chrome !== null;'
    '  out.main.hasChromeRuntime = !!(window.chrome && window.chrome.runtime);'
    '  out.main.ua = navigator.userAgent;'
    '  if (typeof navigator.userAgentData !== "undefined") {'
    '    try {'
    '      const hea = await navigator.userAgentData.getHighEntropyValues(["architecture","platform","platformVersion","bitness"]);'
    '      out.main.hea_arch = hea.architecture;'
    '      out.main.hea_platform = hea.platform;'
    '      out.main.hea_bitness = hea.bitness;'
    '      out.main.hea_platformVersion = hea.platformVersion;'
    '    } catch (e) { out.main.hea_error = String(e); }'
    '  } else { out.main.hea_error = "navigator.userAgentData undefined"; }'
    '  try {'
    '    const w = new Worker("/worker.js");'
    '    out.worker = await new Promise(function(resolve){'
    '      const t = setTimeout(function(){ resolve({ error: "worker timeout" }); }, 5000);'
    '      w.onmessage = function(e){ clearTimeout(t); resolve(e.data); };'
    '      w.onerror = function(e){ clearTimeout(t); resolve({ error: "worker-error: " + (e.message || String(e)) }); };'
    '      w.postMessage("go");'
    '    });'
    '  } catch (e) { out.worker = { error: "worker-spawn: " + String(e) }; }'
    '  document.getElementById("result").textContent = JSON.stringify(out, null, 2);'
    '  try {'
    '    await fetch("/results", { method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out) });'
    '    document.title = "stealth-l2 DONE";'
    '  } catch (e) { document.title = "stealth-l2 POST-FAILED: " + String(e); }'
    '})();'
    '</script></body></html>'
)

ACCEPT_CH = 'Sec-CH-UA-Arch, Sec-CH-UA-Bitness, Sec-CH-UA-Platform-Version, Sec-CH-UA-Platform, Sec-CH-UA-Model, Sec-CH-UA-Full-Version-List'
CRITICAL_CH = 'Sec-CH-UA-Arch, Sec-CH-UA-Bitness, Sec-CH-UA-Platform-Version'

def log_request(handler):
    entry = {
        'url': handler.path,
        'method': handler.command,
        # Lowercase keys so Go assertions can use stable indexing.
        'headers': {k.lower(): v for k, v in handler.headers.items()},
    }
    try:
        with open('/tmp/headers.json', 'a') as f:
            f.write(json.dumps(entry) + '\n')
    except Exception:
        pass

class Handler(BaseHTTPRequestHandler):
    # Silence stderr request log spam; we have our own file log.
    def log_message(self, format, *args): pass

    def do_GET(self):
        log_request(self)
        if self.path in ('/probe', '/'):
            self.send_response(200)
            self.send_header('Content-Type', 'text/html')
            # Accept-CH advertises high-entropy hints; Critical-CH forces
            # Chrome to retry this navigation with them attached.
            self.send_header('Accept-CH', ACCEPT_CH)
            self.send_header('Critical-CH', CRITICAL_CH)
            self.end_headers()
            self.wfile.write(HTML.encode('utf-8'))
        elif self.path == '/worker.js':
            self.send_response(200)
            self.send_header('Content-Type', 'application/javascript')
            self.end_headers()
            self.wfile.write(WORKER_JS.encode('utf-8'))
        else:
            self.send_error(404)

    def do_POST(self):
        log_request(self)
        if self.path == '/results':
            length = int(self.headers.get('Content-Length', '0'))
            body = self.rfile.read(length) if length else b''
            try:
                with open('/tmp/probe-results.json', 'wb') as f:
                    f.write(body)
            except Exception as e:
                print('write probe-results failed:', e, file=sys.stderr)
            self.send_response(204)
            self.end_headers()
        else:
            self.send_error(404)

server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
port = server.server_address[1]
with open('/tmp/server-port.txt', 'w') as f:
    f.write(str(port))
print('ready ' + str(port), flush=True)
server.serve_forever()
`

// stealthProbeClient is a Python MCP stdio client that launches
// patchright-mcp-cell (identical to production launch), navigates Chromium to
// the probe URL, and waits for the probe page to POST its results back to the
// in-container server.
//
// The probe page writes results to /tmp/probe-results.json via the server's
// POST handler — we don't extract values through MCP, only confirm the page
// completed. This avoids the brittle MCP-output parsing path entirely.
const stealthProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time

CHROMIUM = '/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
USER_DATA = '/tmp/pw-stealth-l2'

with open('/tmp/server-port.txt') as f:
    port = f.read().strip()
url = 'http://127.0.0.1:' + port + '/probe'
print('probe url:', url, flush=True)

env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = USER_DATA

proc = subprocess.Popen(
    ['patchright-mcp-cell', '--headless', '--browser', 'chromium',
     '--executable-path', CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    stderr=open('/tmp/mcp-stderr.log', 'w'),
    env=env)

def send(msg):
    proc.stdin.write((json.dumps(msg) + '\n').encode())
    proc.stdin.flush()

def recv():
    line = proc.stdout.readline()
    if not line:
        raise RuntimeError('EOF from patchright-mcp-cell stdout')
    return json.loads(line)

try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{
        'protocolVersion':'2024-11-05','capabilities':{},
        'clientInfo':{'name':'stealth-l2','version':'0'}}})
    r = recv()
    print('init:', r.get('result',{}).get('serverInfo',{}).get('name','?'), flush=True)

    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{
        'name':'browser_navigate','arguments':{'url':url}}})
    r = recv()
    if 'error' in r:
        print('navigate ERROR:', r.get('error'), file=sys.stderr)
        sys.exit(2)
    print('navigate: ok', flush=True)

    # Wait up to 45s for the probe page to POST /results.
    deadline = time.time() + 45
    while time.time() < deadline:
        if os.path.exists('/tmp/probe-results.json') and os.path.getsize('/tmp/probe-results.json') > 10:
            print('DONE', flush=True)
            sys.exit(0)
        time.sleep(0.5)

    print('ERROR: probe page did not POST results within 45s', file=sys.stderr)
    sys.exit(3)
finally:
    proc.terminate()
    try: proc.wait(timeout=5)
    except Exception: proc.kill()
`

// TestStealth_L2_AllLayersConsistent — CELL-153: regression test for
// CELL-161 (silent abort) and CELL-68 (cross-layer arch mismatch).
//
// Sets DEVCELL_STEALTH_ARCH=arm + DEVCELL_STEALTH_PLATFORM=Linux on the
// container, then launches patchright-mcp-cell via MCP stdio and verifies
// that all 4 layers (CDP headers, main JS, Worker JS, sentinel) report
// values consistent with those env vars.
//
// On TDD RED (current broken container per CONTINUE.md), expect failure on:
//   - sentinel: window.__cellStealth === undefined  (script aborted)
//   - main JS: navigator.platform === "Linux x86_64"  (unspoofed)
//   - HTTP: no Sec-CH-UA-Arch advertised  (jq merge / wrapper broken)
//
// On TDD GREEN (after init-script injection fix + CELL-68 unified config):
//   - sentinel: typeof window.__cellStealth === "object"
//   - main JS: navigator.platform === "Linux aarch64"
//   - HTTP: Sec-CH-UA-Arch == "arm"
//   - Worker: same arch + platform as main thread
func TestStealth_L2_AllLayersConsistent(t *testing.T) {
	const (
		archEnv          = "arm"
		platformEnv      = "Linux"
		expectedPlatform = "Linux aarch64" // platformEnv + archEnv per stealth-init.js:395
	)

	c := startContainer(t, map[string]string{
		"HOST_USER":                hostUser,
		"APP_NAME":                 "stealth-l2",
		"DEVCELL_STEALTH_ARCH":     archEnv,
		"DEVCELL_STEALTH_PLATFORM": platformEnv,
		"USER_WORKING_DIR":         "/tmp/stealth-l2-wd",
	})

	// Skip if patchright-mcp-cell isn't installed in this stack variant.
	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH (stack without scraping module)")
	}
	// Skip if python3 isn't available (needed for both probe server and MCP client).
	if _, code := exec(t, c, []string{"sh", "-c", "command -v python3"}); code != 0 {
		t.Skip("python3 not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(stealthProbeServer), "/tmp/stealth-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy probe server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(stealthProbeClient), "/tmp/stealth-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy probe client: %v", err)
	}

	// Reset any prior run artifacts so polling sees a clean slate.
	exec(t, c, []string{"sh", "-c",
		"rm -f /tmp/headers.json /tmp/probe-results.json /tmp/server-port.txt /tmp/mcp-stderr.log /tmp/probe-server.log"})

	// Start the probe server in the background.
	exec(t, c, []string{"bash", "-c",
		"nohup python3 /tmp/stealth-probe-server.py > /tmp/probe-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/server-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		log, _ := exec(t, c, []string{"cat", "/tmp/probe-server.log"})
		t.Fatalf("probe server did not start in 5s:\n%s", log)
	}
	port, _ := exec(t, c, []string{"cat", "/tmp/server-port.txt"})
	t.Logf("probe server listening on port %s", port)

	// Drive patchright-mcp-cell via MCP stdio.
	mcpOut, mcpCode := exec(t, c, []string{"python3", "/tmp/stealth-probe-client.py"})
	t.Logf("MCP client output:\n%s", mcpOut)
	if mcpCode != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/mcp-stderr.log 2>/dev/null"})
		t.Fatalf("MCP client exited %d\nstderr:\n%s", mcpCode, stderr)
	}

	// Read structured probe results (written by probe HTML via POST /results).
	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/probe-results.json"})
	if code != 0 {
		t.Fatal("/tmp/probe-results.json missing — probe HTML did not POST results")
	}
	var results struct {
		Main struct {
			CellStealth        string      `json:"cellStealth"`
			CellStealthValue   string      `json:"cellStealthValue"`
			Platform           string      `json:"platform"`
			WebdriverType      string      `json:"webdriverType"`
			Webdriver          interface{} `json:"webdriver"`
			HasChrome          bool        `json:"hasChrome"`
			HasChromeRuntime   bool        `json:"hasChromeRuntime"`
			UA                 string      `json:"ua"`
			HeaArch            string      `json:"hea_arch"`
			HeaPlatform        string      `json:"hea_platform"`
			HeaBitness         string      `json:"hea_bitness"`
			HeaPlatformVersion string      `json:"hea_platformVersion"`
			HeaError           string      `json:"hea_error"`
		} `json:"main"`
		Worker struct {
			Platform    string `json:"platform"`
			UA          string `json:"ua"`
			CellStealth string `json:"cellStealth"`
			HeaArch     string `json:"hea_arch"`
			HeaPlatform string `json:"hea_platform"`
			HeaBitness  string `json:"hea_bitness"`
			HeaError    string `json:"hea_error"`
			Error       string `json:"error"`
		} `json:"worker"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &results); err != nil {
		t.Fatalf("parse probe-results.json: %v\nraw: %s", err, resultsRaw)
	}
	t.Logf("probe-results.json (parsed):\n  main=%+v\n  worker=%+v", results.Main, results.Worker)

	// Read recorded HTTP headers and find the last request with the high-entropy hints.
	headersRaw, _ := exec(t, c, []string{"cat", "/tmp/headers.json"})
	var archHeader, platformHeader, bitnessHeader, uaHeader string
	bitnessSeen := false
	for _, line := range strings.Split(strings.TrimSpace(headersRaw), "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if v := entry.Headers["sec-ch-ua-arch"]; v != "" {
			archHeader = strings.Trim(v, `"`)
		}
		if v := entry.Headers["sec-ch-ua-platform"]; v != "" {
			platformHeader = strings.Trim(v, `"`)
		}
		// Sec-CH-UA-Bitness: header is present when Chrome honored Accept-CH.
		// An empty value (`""`) is the broken case (CELL-150) vs `"64"` GREEN.
		if v, ok := entry.Headers["sec-ch-ua-bitness"]; ok {
			bitnessHeader = strings.Trim(v, `"`)
			bitnessSeen = true
		}
		if v := entry.Headers["user-agent"]; v != "" {
			uaHeader = v
		}
	}
	t.Logf("HTTP headers observed: Sec-CH-UA-Arch=%q  Sec-CH-UA-Platform=%q  Sec-CH-UA-Bitness=%q(seen=%v)  User-Agent=%q",
		archHeader, platformHeader, bitnessHeader, bitnessSeen, uaHeader)

	// ── Layer 0 — sentinel ─────────────────────────────────────────────────
	// If __cellStealth is undefined, the wrapper's --init-script preamble
	// never reached the page context. This is the CELL-161 silent-abort
	// signature and the single most important assertion in this file.
	if results.Main.CellStealth == "undefined" || results.Main.CellStealth == "" {
		t.Errorf("FAIL Layer 0 (sentinel): typeof window.__cellStealth=%q, want %q — "+
			"stealth-init.js / __cellStealth preamble never reached the page (CELL-161 regression class)",
			results.Main.CellStealth, "object")
	} else {
		t.Logf("PASS Layer 0: __cellStealth=%s  value=%s", results.Main.CellStealth, results.Main.CellStealthValue)
	}

	// ── Layer 1 — CDP HTTP headers ─────────────────────────────────────────
	if archHeader == "" {
		t.Errorf("FAIL Layer 1: no Sec-CH-UA-Arch header observed in any request — "+
			"either jq merge of userAgentMetadata failed (CELL-68), or Chrome didn't honor Accept-CH/Critical-CH. "+
			"recorded headers:\n%s", lastNLines(headersRaw, 20))
	} else if archHeader != archEnv {
		t.Errorf("FAIL Layer 1: Sec-CH-UA-Arch=%q, want %q (CELL-68 cross-layer arch mismatch)",
			archHeader, archEnv)
	} else {
		t.Logf("PASS Layer 1: Sec-CH-UA-Arch=%q matches DEVCELL_STEALTH_ARCH", archHeader)
	}
	if platformHeader != "" && platformHeader != platformEnv {
		t.Errorf("FAIL Layer 1: Sec-CH-UA-Platform=%q, want %q", platformHeader, platformEnv)
	}
	// CELL-150: Sec-CH-UA-Bitness must be "64" (real desktop Chrome). Empty
	// string was observed in the broken state — anomalous for any x86/arm64
	// desktop and a strong fingerprint.
	if !bitnessSeen {
		t.Errorf("FAIL Layer 1: Sec-CH-UA-Bitness header never sent — Accept-CH not honored, or wrapper not advertising it (CELL-150)")
	} else if bitnessHeader != "64" {
		t.Errorf("FAIL Layer 1: Sec-CH-UA-Bitness=%q, want %q (CELL-150: empty string is the leak signature)",
			bitnessHeader, "64")
	} else {
		t.Logf("PASS Layer 1: Sec-CH-UA-Bitness=%q", bitnessHeader)
	}

	// ── Layer 2 — main-thread JS ───────────────────────────────────────────
	if results.Main.Platform != expectedPlatform {
		t.Errorf("FAIL Layer 2: navigator.platform=%q, want %q (CELL-68 main-thread spoof)",
			results.Main.Platform, expectedPlatform)
	} else {
		t.Logf("PASS Layer 2: navigator.platform=%q", results.Main.Platform)
	}
	// navigator.webdriver must NOT be `true`. Both `undefined` (patchright's
	// preferred spoof — property deleted entirely) and `false` (real Chrome
	// non-automated value) are acceptable. The existing TestMcp_PatchrightUndetected
	// at mcp_test.go:317 documents `undefined` as the expected stealth output.
	if results.Main.Webdriver == true {
		t.Errorf("FAIL Layer 2: navigator.webdriver=true — browser detected as automated (CELL-169)")
	} else {
		t.Logf("PASS Layer 2: navigator.webdriver=%v (type=%s)", results.Main.Webdriver, results.Main.WebdriverType)
	}
	if !results.Main.HasChromeRuntime {
		t.Errorf("FAIL Layer 2: window.chrome.runtime missing (CELL-169 arm64 regression); hasChrome=%v",
			results.Main.HasChrome)
	} else {
		t.Logf("PASS Layer 2: window.chrome.runtime present")
	}
	if results.Main.HeaArch == "" {
		t.Errorf("FAIL Layer 2: getHighEntropyValues().architecture empty — main-thread spoof not running (CELL-68). hea_error=%q",
			results.Main.HeaError)
	} else if results.Main.HeaArch != archEnv {
		t.Errorf("FAIL Layer 2: getHighEntropyValues().architecture=%q, want %q (CELL-68)",
			results.Main.HeaArch, archEnv)
	} else {
		t.Logf("PASS Layer 2: hea.architecture=%q matches env", results.Main.HeaArch)
	}
	// CELL-150: main-thread bitness — must be "64" (matches HTTP layer).
	if results.Main.HeaBitness != "64" {
		t.Errorf("FAIL Layer 2: getHighEntropyValues().bitness=%q, want %q (CELL-150)",
			results.Main.HeaBitness, "64")
	} else {
		t.Logf("PASS Layer 2: hea.bitness=%q", results.Main.HeaBitness)
	}

	// ── Layer 3 — Worker JS ────────────────────────────────────────────────
	if results.Worker.Error != "" {
		t.Errorf("FAIL Layer 3: Worker probe failed: %q (workers must be reachable for cross-context consistency)",
			results.Worker.Error)
	} else {
		if results.Worker.Platform != expectedPlatform {
			t.Errorf("FAIL Layer 3: Worker navigator.platform=%q, want %q (CELL-34)",
				results.Worker.Platform, expectedPlatform)
		} else {
			t.Logf("PASS Layer 3: Worker navigator.platform=%q matches main", results.Worker.Platform)
		}
		if results.Worker.HeaArch == "" {
			t.Errorf("FAIL Layer 3: Worker getHighEntropyValues().architecture empty (CELL-25). hea_error=%q",
				results.Worker.HeaError)
		} else if results.Worker.HeaArch != archEnv {
			t.Errorf("FAIL Layer 3: Worker hea.architecture=%q, want %q (CELL-25)",
				results.Worker.HeaArch, archEnv)
		} else {
			t.Logf("PASS Layer 3: Worker hea.architecture=%q matches env", results.Worker.HeaArch)
		}
		// CELL-150: Worker bitness — must match main (consistency across contexts).
		if results.Worker.HeaBitness != "64" {
			t.Errorf("FAIL Layer 3: Worker getHighEntropyValues().bitness=%q, want %q (CELL-150 worker propagation)",
				results.Worker.HeaBitness, "64")
		} else {
			t.Logf("PASS Layer 3: Worker hea.bitness=%q matches main", results.Worker.HeaBitness)
		}
	}

	_ = uaHeader // recorded for debug logging
}
