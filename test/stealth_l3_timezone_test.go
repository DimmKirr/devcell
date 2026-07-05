package container_test

// stealth_l3_timezone_test.go — CELL-21: Timezone vs IP geolocation
// mismatch. Currently JS Intl.timeZone reports UTC ("Africa/Abidjan")
// while the container's egress IP geolocates to Czechia (Europe/Prague).
// Pixelscan flags this as the #1 inconsistency.
//
// Drives patchright-mcp-cell via MCP stdio (same path as
// stealth_l2_test.go) — not a unit test, full real-MCP coverage.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const tzProbeServer = `#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HTML = (
    '<!DOCTYPE html><html><head><title>tz</title></head><body><script>'
    '(async function(){'
    '  const out = {};'
    '  out.intlTz = Intl.DateTimeFormat().resolvedOptions().timeZone;'
    '  out.tzOffset = new Date().getTimezoneOffset();'
    '  out.dateStr = new Date().toString();'
    '  out.jsEpochMs = Date.now();'
    '  try {'
    '    const w = new Worker(URL.createObjectURL(new Blob(['
    '      "self.onmessage=()=>{self.postMessage({wTz:Intl.DateTimeFormat().resolvedOptions().timeZone,wOff:new Date().getTimezoneOffset()})}"'
    '    ], {type: "application/javascript"})));'
    '    out.worker = await new Promise(function(r){'
    '      const t = setTimeout(function(){ r({error:"timeout"}); }, 3000);'
    '      w.onmessage = function(e){ clearTimeout(t); r(e.data); };'
    '      w.postMessage("go");'
    '    });'
    '  } catch (e) { out.worker = { error: String(e) }; }'
    '  await fetch("/results", {method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out)});'
    '  document.title = "tz DONE";'
    '})();'
    '</script></body></html>'
)

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        if self.path in ('/probe','/'):
            self.send_response(200); self.send_header('Content-Type','text/html'); self.end_headers()
            self.wfile.write(HTML.encode())
        else: self.send_error(404)
    def do_POST(self):
        if self.path == '/results':
            n = int(self.headers.get('Content-Length','0'))
            with open('/tmp/tz-results.json','wb') as f: f.write(self.rfile.read(n))
            self.send_response(204); self.end_headers()
        else: self.send_error(404)

s = ThreadingHTTPServer(('127.0.0.1', 0), H)
with open('/tmp/tz-port.txt','w') as f: f.write(str(s.server_address[1]))
print('ready', flush=True)
s.serve_forever()
`

const tzProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time
CHROMIUM='/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
port=open('/tmp/tz-port.txt').read().strip()
url='http://127.0.0.1:'+port+'/probe'
env=dict(os.environ); env['PLAYWRIGHT_MCP_USER_DATA_DIR']='/tmp/pw-tz-l3'
p=subprocess.Popen(['patchright-mcp-cell','--headless','--browser','chromium','--executable-path',CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open('/tmp/tz-mcp-stderr.log','w'), env=env)
def send(m): p.stdin.write((json.dumps(m)+'\n').encode()); p.stdin.flush()
def recv(): return json.loads(p.stdout.readline())
try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'tz','version':'0'}}}); recv()
    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'browser_navigate','arguments':{'url':url}}})
    r=recv()
    if 'error' in r: print('nav err',r,file=sys.stderr); sys.exit(2)
    d=time.time()+45
    while time.time()<d:
        if os.path.exists('/tmp/tz-results.json') and os.path.getsize('/tmp/tz-results.json')>10:
            print('DONE',flush=True); sys.exit(0)
        time.sleep(0.5)
    sys.exit(3)
finally:
    p.terminate()
    try: p.wait(timeout=5)
    except Exception: p.kill()
`

// TestStealth_L3_TimezoneMatchesContainer — CELL-21. Asserts JS
// Intl.timeZone matches container TZ env var. Expected RED: container
// has no TZ set, JS reports "UTC" / "Africa/Abidjan".
func TestStealth_L3_TimezoneMatchesContainer(t *testing.T) {
	const expectedTZ = "Europe/Prague"

	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "tz-l3",
		"TZ":               expectedTZ,
		"USER_WORKING_DIR": "/tmp/tz-l3-wd",
	})
	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(tzProbeServer), "/tmp/tz-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(tzProbeClient), "/tmp/tz-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy client: %v", err)
	}

	exec(t, c, []string{"sh", "-c", "rm -f /tmp/tz-*.json /tmp/tz-port.txt /tmp/tz-mcp-stderr.log /tmp/tz-server.log"})
	exec(t, c, []string{"bash", "-c", "nohup python3 /tmp/tz-probe-server.py > /tmp/tz-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/tz-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		t.Fatal("probe server start")
	}

	out, code := exec(t, c, []string{"python3", "/tmp/tz-probe-client.py"})
	t.Logf("MCP:\n%s", out)
	if code != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/tz-mcp-stderr.log"})
		t.Fatalf("MCP exit %d\n%s", code, stderr)
	}

	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/tz-results.json"})
	if code != 0 {
		t.Fatal("/tmp/tz-results.json missing")
	}
	var r struct {
		IntlTz    string `json:"intlTz"`
		TzOffset  int    `json:"tzOffset"`
		DateStr   string `json:"dateStr"`
		JsEpochMs int64  `json:"jsEpochMs"`
		Worker    struct {
			WTz   string `json:"wTz"`
			WOff  int    `json:"wOff"`
			Error string `json:"error"`
		} `json:"worker"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &r); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, resultsRaw)
	}
	t.Logf("probe: intlTz=%q tzOffset=%d dateStr=%q worker=%+v",
		r.IntlTz, r.TzOffset, r.DateStr, r.Worker)

	if r.IntlTz != expectedTZ {
		t.Errorf("FAIL: Intl.DateTimeFormat().resolvedOptions().timeZone=%q, want %q "+
			"(container TZ=%q ignored — JS sees UTC, IP geolocates Czechia → pixelscan flags as spoofed)",
			r.IntlTz, expectedTZ, expectedTZ)
	}
	// Europe/Prague is UTC+1 (or +2 in DST). getTimezoneOffset is INVERTED
	// (returns -60 or -120 minutes). UTC returns 0.
	if r.TzOffset == 0 {
		t.Errorf("FAIL: Date().getTimezoneOffset()=0 (UTC); container TZ=%s should produce non-zero offset",
			expectedTZ)
	}
	if r.Worker.Error == "" && r.Worker.WTz != expectedTZ {
		t.Errorf("FAIL: Worker Intl.timeZone=%q, want %q (Worker context not honoring TZ)",
			r.Worker.WTz, expectedTZ)
	}
	if r.Worker.Error == "" && r.Worker.WTz != r.IntlTz {
		t.Errorf("FAIL: Worker TZ %q != main TZ %q (cross-context inconsistency)",
			r.Worker.WTz, r.IntlTz)
	}
	if strings.Contains(r.DateStr, "GMT+0000") && expectedTZ != "UTC" {
		t.Errorf("FAIL: Date.toString()=%q reports GMT+0000 despite TZ=%q", r.DateStr, expectedTZ)
	}
}
