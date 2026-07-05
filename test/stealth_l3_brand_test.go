package container_test

// stealth_l3_brand_test.go — DIMM-XXX: Google Chrome brand presence.
//
// Detection-site sweep (creepjs, browserleaks, pixelscan) showed our
// Sec-CH-UA reports only `"Chromium";v="141", "Not?A_Brand";v="8"`.
// Real Chrome adds `"Google Chrome";v="141"` — its absence is one of the
// strongest signals Google uses at the identifier step ("Couldn't sign you
// in — this browser or app may not be secure").
//
// This test drives patchright-mcp-cell via MCP stdio (same path as
// stealth_l2_test.go) and asserts BOTH:
//   - HTTP `Sec-CH-UA` header includes "Google Chrome"
//   - JS `navigator.userAgentData.brands[].brand` includes "Google Chrome"
//
// Expected RED on current build: patchright-core's `_calculateBrandsList`
// omits "Google Chrome" — same class of patch as CELL-150 (nix postPatch
// on coreBundle.js).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const brandProbeServer = `#!/usr/bin/env python3
import json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HTML = (
    '<!DOCTYPE html><html><head><title>brand-probe</title></head><body>'
    '<pre id="r">pending</pre><script>(async function(){'
    '  const out = {};'
    '  out.ua = navigator.userAgent;'
    '  if (navigator.userAgentData) {'
    '    out.brands = navigator.userAgentData.brands;'
    '    try {'
    '      const h = await navigator.userAgentData.getHighEntropyValues(["fullVersionList"]);'
    '      out.fullVersionList = h.fullVersionList;'
    '    } catch (e) { out.fvl_error = String(e); }'
    '  } else { out.error = "userAgentData undefined"; }'
    '  document.getElementById("r").textContent = JSON.stringify(out, null, 2);'
    '  try {'
    '    await fetch("/results", { method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out) });'
    '    document.title = "brand DONE";'
    '  } catch (e) { document.title = "brand FAIL: " + String(e); }'
    '})();</script></body></html>'
)

ACCEPT_CH = 'Sec-CH-UA-Full-Version-List, Sec-CH-UA-Full-Version'
CRITICAL_CH = 'Sec-CH-UA-Full-Version-List'

def log_request(handler):
    try:
        with open('/tmp/brand-headers.json', 'a') as f:
            f.write(json.dumps({
                'url': handler.path,
                'headers': {k.lower(): v for k, v in handler.headers.items()},
            }) + '\n')
    except Exception:
        pass

class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args): pass
    def do_GET(self):
        log_request(self)
        if self.path in ('/probe', '/'):
            self.send_response(200)
            self.send_header('Content-Type', 'text/html')
            self.send_header('Accept-CH', ACCEPT_CH)
            self.send_header('Critical-CH', CRITICAL_CH)
            self.end_headers()
            self.wfile.write(HTML.encode('utf-8'))
        else:
            self.send_error(404)
    def do_POST(self):
        log_request(self)
        if self.path == '/results':
            length = int(self.headers.get('Content-Length', '0'))
            body = self.rfile.read(length) if length else b''
            with open('/tmp/brand-results.json', 'wb') as f:
                f.write(body)
            self.send_response(204)
            self.end_headers()
        else:
            self.send_error(404)

server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
port = server.server_address[1]
with open('/tmp/brand-port.txt', 'w') as f:
    f.write(str(port))
print('ready ' + str(port), flush=True)
server.serve_forever()
`

const brandProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time

with open('/tmp/brand-port.txt') as f:
    port = f.read().strip()
url = 'http://127.0.0.1:' + port + '/probe'
print('probe url:', url, flush=True)

env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = '/tmp/pw-brand-l3'
env['DISPLAY'] = ':99'

# Production-mode launch: NO --headless, NO --executable-path. Lets
# patchright use its bundled chromium and headed Xvfb display, same as
# 'cell claude' (CELL-17).
proc = subprocess.Popen(
    ['patchright-mcp-cell', '--browser', 'chromium'],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    stderr=open('/tmp/brand-mcp-stderr.log', 'w'),
    env=env)

def send(msg):
    proc.stdin.write((json.dumps(msg) + '\n').encode())
    proc.stdin.flush()
def recv():
    line = proc.stdout.readline()
    if not line: raise RuntimeError('EOF')
    return json.loads(line)

try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{
        'protocolVersion':'2024-11-05','capabilities':{},
        'clientInfo':{'name':'brand-l3','version':'0'}}})
    recv()
    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{
        'name':'browser_navigate','arguments':{'url':url}}})
    r = recv()
    if 'error' in r:
        print('navigate ERROR:', r.get('error'), file=sys.stderr); sys.exit(2)
    deadline = time.time() + 45
    while time.time() < deadline:
        if os.path.exists('/tmp/brand-results.json') and os.path.getsize('/tmp/brand-results.json') > 10:
            print('DONE', flush=True); sys.exit(0)
        time.sleep(0.5)
    print('TIMEOUT', file=sys.stderr); sys.exit(3)
finally:
    proc.terminate()
    try: proc.wait(timeout=5)
    except Exception: proc.kill()
`

// TestStealth_L3_GoogleChromeBrand — DIMM-XXX: Sec-CH-UA must include
// "Google Chrome" brand. patchright-core currently omits it, which is a
// strong fingerprint Google uses to reject sign-in.
func TestStealth_L3_GoogleChromeBrand(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "brand-l3",
		"USER_WORKING_DIR": "/tmp/brand-l3-wd",
	})

	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH")
	}
	if _, code := exec(t, c, []string{"sh", "-c", "command -v python3"}); code != 0 {
		t.Skip("python3 not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(brandProbeServer), "/tmp/brand-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(brandProbeClient), "/tmp/brand-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy client: %v", err)
	}

	exec(t, c, []string{"sh", "-c",
		"rm -f /tmp/brand-headers.json /tmp/brand-results.json /tmp/brand-port.txt /tmp/brand-mcp-stderr.log /tmp/brand-server.log /tmp/.X99-lock /tmp/.X11-unix/X99"})

	// Production-mode launch needs Xvfb on :99 — testcontainers' tail
	// Cmd bypasses entrypoint so 50-gui.sh never runs (CELL-17).
	exec(t, c, []string{"bash", "-c",
		"setsid Xvfb :99 -screen 0 1920x1080x24 +extension GLX +render < /dev/null > /tmp/Xvfb.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in $(seq 1 40); do DISPLAY=:99 xset q >/dev/null 2>&1 && exit 0; sleep 0.25; done; cat /tmp/Xvfb.log; exit 1",
	}); code != 0 {
		t.Fatal("Xvfb did not come up on :99")
	}

	exec(t, c, []string{"bash", "-c",
		"nohup python3 /tmp/brand-probe-server.py > /tmp/brand-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/brand-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		log, _ := exec(t, c, []string{"cat", "/tmp/brand-server.log"})
		t.Fatalf("probe server start: %s", log)
	}

	mcpOut, mcpCode := exec(t, c, []string{"python3", "/tmp/brand-probe-client.py"})
	t.Logf("MCP client:\n%s", mcpOut)
	if mcpCode != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/brand-mcp-stderr.log"})
		t.Fatalf("MCP exit %d\n%s", mcpCode, stderr)
	}

	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/brand-results.json"})
	if code != 0 {
		t.Fatal("/tmp/brand-results.json missing")
	}
	var results struct {
		UA     string `json:"ua"`
		Brands []struct {
			Brand   string `json:"brand"`
			Version string `json:"version"`
		} `json:"brands"`
		FullVersionList []struct {
			Brand   string `json:"brand"`
			Version string `json:"version"`
		} `json:"fullVersionList"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &results); err != nil {
		t.Fatalf("parse results: %v\nraw: %s", err, resultsRaw)
	}
	t.Logf("probe results: ua=%q brands=%+v fullVersionList=%+v",
		results.UA, results.Brands, results.FullVersionList)

	headersRaw, _ := exec(t, c, []string{"cat", "/tmp/brand-headers.json"})
	var secChUA string
	for _, line := range strings.Split(strings.TrimSpace(headersRaw), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if v := e.Headers["sec-ch-ua"]; v != "" {
			secChUA = v
		}
	}
	t.Logf("HTTP Sec-CH-UA=%q", secChUA)

	// ── Assertion 1: JS userAgentData.brands includes "Google Chrome" ──
	var jsBrands []string
	for _, b := range results.Brands {
		jsBrands = append(jsBrands, b.Brand)
	}
	hasGoogleChromeJS := false
	for _, b := range jsBrands {
		if b == "Google Chrome" {
			hasGoogleChromeJS = true
			break
		}
	}
	if !hasGoogleChromeJS {
		t.Errorf("FAIL: navigator.userAgentData.brands missing %q — got %v. "+
			"Real Chrome always advertises Google Chrome brand; absence is a Google sign-in rejection signal.",
			"Google Chrome", jsBrands)
	} else {
		t.Logf("PASS: brands include %q", "Google Chrome")
	}

	// ── Assertion 2: HTTP Sec-CH-UA header contains "Google Chrome" ──
	if secChUA == "" {
		t.Error("FAIL: no Sec-CH-UA header observed in any request — Accept-CH not honored?")
	} else if !strings.Contains(secChUA, "Google Chrome") {
		t.Errorf("FAIL: Sec-CH-UA=%q missing %q brand. Real Chrome sends "+
			`"Google Chrome";v="141", "Chromium";v="141", "Not?A_Brand";v="8". `+
			"This is the patchright-core _calculateBrandsList omission.",
			secChUA, "Google Chrome")
	} else {
		t.Logf("PASS: Sec-CH-UA contains Google Chrome")
	}

	// ── Assertion 3: fullVersionList populated with Google Chrome ──
	hasGoogleChromeFVL := false
	for _, b := range results.FullVersionList {
		if b.Brand == "Google Chrome" {
			hasGoogleChromeFVL = true
			break
		}
	}
	if len(results.FullVersionList) == 0 {
		t.Errorf("FAIL: fullVersionList is empty — getHighEntropyValues didn't return brand versions (real Chrome populates this).")
	} else if !hasGoogleChromeFVL {
		var fvlBrands []string
		for _, b := range results.FullVersionList {
			fvlBrands = append(fvlBrands, b.Brand)
		}
		t.Errorf("FAIL: fullVersionList missing %q brand; got %v", "Google Chrome", fvlBrands)
	} else {
		t.Logf("PASS: fullVersionList includes Google Chrome")
	}
}
