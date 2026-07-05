package container_test

// stealth_l3_permissions_test.go — CELL-19: Permissions API.
// Real Chrome returns PermissionStatus for most queries (state "prompt"
// or "granted"). Ours returns "Not supported" for ~15 of them. amiunique
// scores this at 0.03% similarity — essentially unique.

import (
	"context"
	"encoding/json"
	"testing"
)

const permsProbeServer = `#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HTML = (
    '<!DOCTYPE html><html><head><title>perms</title></head><body><script>'
    '(async function(){'
    '  const names = ["geolocation","notifications","camera","microphone",'
    '    "clipboard-read","clipboard-write","background-sync","payment-handler",'
    '    "persistent-storage","push","midi","accelerometer","gyroscope",'
    '    "magnetometer","ambient-light-sensor"];'
    '  const out = { perms: {} };'
    '  if (!navigator.permissions || !navigator.permissions.query) {'
    '    out.error = "navigator.permissions missing";'
    '  } else {'
    '    for (const n of names) {'
    '      try {'
    '        const p = await navigator.permissions.query({ name: n });'
    '        out.perms[n] = { state: p.state, ok: true };'
    '      } catch (e) {'
    '        out.perms[n] = { state: null, ok: false, error: String(e).slice(0, 200) };'
    '      }'
    '    }'
    '  }'
    '  await fetch("/results", {method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out)});'
    '  document.title = "perms DONE";'
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
            with open('/tmp/perms-results.json','wb') as f: f.write(self.rfile.read(n))
            self.send_response(204); self.end_headers()
        else: self.send_error(404)

s = ThreadingHTTPServer(('127.0.0.1', 0), H)
with open('/tmp/perms-port.txt','w') as f: f.write(str(s.server_address[1]))
print('ready', flush=True)
s.serve_forever()
`

const permsProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time
CHROMIUM='/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
url='http://127.0.0.1:'+open('/tmp/perms-port.txt').read().strip()+'/probe'
env=dict(os.environ); env['PLAYWRIGHT_MCP_USER_DATA_DIR']='/tmp/pw-perms-l3'; env['DISPLAY']=':99'
# NO --headless — production runs Xvfb-headed via DISPLAY=:99 (CELL-17).
# Headless mode disables most permission backends, returning "Illegal
# invocation" for queries that DO work in real production Chrome.
p=subprocess.Popen(['patchright-mcp-cell','--browser','chromium','--executable-path',CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open('/tmp/perms-mcp-stderr.log','w'), env=env)
def send(m): p.stdin.write((json.dumps(m)+'\n').encode()); p.stdin.flush()
def recv(): return json.loads(p.stdout.readline())
try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'perms','version':'0'}}}); recv()
    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'browser_navigate','arguments':{'url':url}}})
    r=recv()
    if 'error' in r: print('nav err',r,file=sys.stderr); sys.exit(2)
    d=time.time()+45
    while time.time()<d:
        if os.path.exists('/tmp/perms-results.json') and os.path.getsize('/tmp/perms-results.json')>10:
            print('DONE',flush=True); sys.exit(0)
        time.sleep(0.5)
    sys.exit(3)
finally:
    p.terminate()
    try: p.wait(timeout=5)
    except Exception: p.kill()
`

// TestStealth_L3_PermissionsAPISupported — CELL-19. Expected RED:
// ~15 permission queries either throw or return "Not supported".
// Must be ≥10 of 15 queryable (returning a state) to pass.
func TestStealth_L3_PermissionsAPISupported(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "perms-l3",
		"USER_WORKING_DIR": "/tmp/perms-l3-wd",
	})
	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(permsProbeServer), "/tmp/perms-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(permsProbeClient), "/tmp/perms-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy client: %v", err)
	}

	exec(t, c, []string{"sh", "-c", "rm -f /tmp/perms-*.json /tmp/perms-port.txt /tmp/perms-mcp-stderr.log /tmp/perms-server.log /tmp/.X99-lock /tmp/.X11-unix/X99"})

	// Start Xvfb manually — testcontainers' `tail -f /dev/null` Cmd bypasses
	// the entrypoint script, so 50-gui.sh never runs. We must replicate
	// what production does: spawn Xvfb on :99 so chromium has a real
	// display surface (and most permission backends activate).
	exec(t, c, []string{"bash", "-c",
		"setsid Xvfb :99 -screen 0 1920x1080x24 +extension GLX +render < /dev/null > /tmp/Xvfb.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in $(seq 1 40); do DISPLAY=:99 xset q >/dev/null 2>&1 && exit 0; sleep 0.25; done; cat /tmp/Xvfb.log; exit 1",
	}); code != 0 {
		t.Fatal("Xvfb did not come up on :99")
	}

	exec(t, c, []string{"bash", "-c", "nohup python3 /tmp/perms-probe-server.py > /tmp/perms-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/perms-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		t.Fatal("probe server start")
	}

	out, code := exec(t, c, []string{"python3", "/tmp/perms-probe-client.py"})
	t.Logf("MCP:\n%s", out)
	if code != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/perms-mcp-stderr.log"})
		t.Fatalf("MCP exit %d\n%s", code, stderr)
	}

	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/perms-results.json"})
	if code != 0 {
		t.Fatal("/tmp/perms-results.json missing")
	}
	var r struct {
		Error string `json:"error"`
		Perms map[string]struct {
			State string `json:"state"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"perms"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &r); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, resultsRaw)
	}
	if r.Error != "" {
		t.Fatalf("FAIL: %s", r.Error)
	}

	queryable := 0
	failing := []string{}
	for name, p := range r.Perms {
		if p.OK && p.State != "" {
			queryable++
		} else {
			failing = append(failing, name+":"+p.Error)
		}
	}
	t.Logf("probe: %d/%d permissions queryable; failing: %v", queryable, len(r.Perms), failing)

	// Core 5: geo, notifications, camera, microphone, push — should ALWAYS query.
	core := []string{"geolocation", "notifications", "camera", "microphone", "push"}
	for _, name := range core {
		p, ok := r.Perms[name]
		if !ok {
			t.Errorf("FAIL: %q not in results — probe didn't query?", name)
			continue
		}
		if !p.OK || p.State == "" {
			t.Errorf("FAIL: navigator.permissions.query({name:%q}) failed/Not supported (state=%q, err=%q). "+
				"Real Chrome ALWAYS returns a PermissionStatus for this. amiunique 0.03%% similarity → uniquely identifying.",
				name, p.State, p.Error)
		}
	}

	// Bulk: at least 10 of 15 should succeed.
	if queryable < 10 {
		t.Errorf("FAIL: only %d/%d permissions queryable, want ≥10. "+
			"creepjs reports `permissions (0)` — zero queryable permissions is uniquely identifying.",
			queryable, len(r.Perms))
	}
}
