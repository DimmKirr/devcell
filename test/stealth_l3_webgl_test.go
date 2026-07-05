package container_test

// stealth_l3_webgl_test.go — CELL-70: Main-thread WebGL strings.
// Currently reports "Intel Inc." / "Intel Iris OpenGL Engine" — macOS
// strings on Linux aarch64. Drives patchright-mcp-cell via MCP stdio.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const webglProbeServer = `#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HTML = (
    '<!DOCTYPE html><html><head><title>webgl</title></head><body>'
    '<canvas id="c" width="64" height="64"></canvas><script>'
    '(async function(){'
    '  const out = {};'
    '  const gl = document.getElementById("c").getContext("webgl") || document.getElementById("c").getContext("experimental-webgl");'
    '  if (!gl) { out.error = "no WebGL"; }'
    '  else {'
    '    const dbg = gl.getExtension("WEBGL_debug_renderer_info");'
    '    out.unmaskedVendor = dbg ? gl.getParameter(dbg.UNMASKED_VENDOR_WEBGL) : "no_ext";'
    '    out.unmaskedRenderer = dbg ? gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) : "no_ext";'
    '    out.vendor = gl.getParameter(gl.VENDOR);'
    '    out.renderer = gl.getParameter(gl.RENDERER);'
    '    out.version = gl.getParameter(gl.VERSION);'
    '    out.shadingLang = gl.getParameter(gl.SHADING_LANGUAGE_VERSION);'
    '  }'
    '  out.platform = navigator.platform;'
    '  await fetch("/results", {method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out)});'
    '  document.title = "webgl DONE";'
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
            with open('/tmp/webgl-results.json','wb') as f: f.write(self.rfile.read(n))
            self.send_response(204); self.end_headers()
        else: self.send_error(404)

s = ThreadingHTTPServer(('127.0.0.1', 0), H)
with open('/tmp/webgl-port.txt','w') as f: f.write(str(s.server_address[1]))
print('ready', flush=True)
s.serve_forever()
`

const webglProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time
url='http://127.0.0.1:'+open('/tmp/webgl-port.txt').read().strip()+'/probe'
env=dict(os.environ); env['PLAYWRIGHT_MCP_USER_DATA_DIR']='/tmp/pw-webgl-l3'; env['DISPLAY']=':99'
# Production-mode launch: NO --headless, NO --executable-path. Lets
# patchright-mcp-cell use its bundled chromium (the same one Claude Code's
# 'playwright' MCP uses). Forcing --executable-path to nix chromium yields
# 'no WebGL' and masks the actual production fingerprint.
p=subprocess.Popen(['patchright-mcp-cell','--browser','chromium'],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open('/tmp/webgl-mcp-stderr.log','w'), env=env)
def send(m): p.stdin.write((json.dumps(m)+'\n').encode()); p.stdin.flush()
def recv(): return json.loads(p.stdout.readline())
try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'webgl','version':'0'}}}); recv()
    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'browser_navigate','arguments':{'url':url}}})
    r=recv()
    if 'error' in r: print('nav err',r,file=sys.stderr); sys.exit(2)
    d=time.time()+45
    while time.time()<d:
        if os.path.exists('/tmp/webgl-results.json') and os.path.getsize('/tmp/webgl-results.json')>10:
            print('DONE',flush=True); sys.exit(0)
        time.sleep(0.5)
    sys.exit(3)
finally:
    p.terminate()
    try: p.wait(timeout=5)
    except Exception: p.kill()
`

// TestStealth_L3_MainThreadWebGLMatchesPlatform — CELL-70. Expected
// RED: WebGL strings are "Intel Inc." / "Intel Iris OpenGL Engine" (Mac
// style) on Linux aarch64.
func TestStealth_L3_MainThreadWebGLMatchesPlatform(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "webgl-l3",
		"USER_WORKING_DIR": "/tmp/webgl-l3-wd",
	})
	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(webglProbeServer), "/tmp/webgl-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(webglProbeClient), "/tmp/webgl-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy client: %v", err)
	}

	exec(t, c, []string{"sh", "-c", "rm -f /tmp/webgl-*.json /tmp/webgl-port.txt /tmp/webgl-mcp-stderr.log /tmp/webgl-server.log /tmp/.X99-lock /tmp/.X11-unix/X99"})

	// Start Xvfb manually — testcontainers' `tail -f /dev/null` Cmd bypasses
	// the entrypoint script, so 50-gui.sh never runs. We must replicate
	// what production does: spawn Xvfb on :99 so chromium has a GL surface.
	exec(t, c, []string{"bash", "-c",
		"setsid Xvfb :99 -screen 0 1920x1080x24 +extension GLX +render < /dev/null > /tmp/Xvfb.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in $(seq 1 40); do DISPLAY=:99 xset q >/dev/null 2>&1 && exit 0; sleep 0.25; done; cat /tmp/Xvfb.log; exit 1",
	}); code != 0 {
		t.Fatal("Xvfb did not come up on :99")
	}

	exec(t, c, []string{"bash", "-c", "nohup python3 /tmp/webgl-probe-server.py > /tmp/webgl-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/webgl-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		t.Fatal("probe server start")
	}

	out, code := exec(t, c, []string{"python3", "/tmp/webgl-probe-client.py"})
	t.Logf("MCP:\n%s", out)
	if code != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/webgl-mcp-stderr.log"})
		t.Fatalf("MCP exit %d\n%s", code, stderr)
	}

	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/webgl-results.json"})
	if code != 0 {
		t.Fatal("/tmp/webgl-results.json missing")
	}
	var r struct {
		UnmaskedVendor   string `json:"unmaskedVendor"`
		UnmaskedRenderer string `json:"unmaskedRenderer"`
		Vendor           string `json:"vendor"`
		Renderer         string `json:"renderer"`
		Version          string `json:"version"`
		ShadingLang      string `json:"shadingLang"`
		Platform         string `json:"platform"`
		Error            string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &r); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, resultsRaw)
	}
	t.Logf("probe: platform=%q unmaskedVendor=%q unmaskedRenderer=%q vendor=%q renderer=%q",
		r.Platform, r.UnmaskedVendor, r.UnmaskedRenderer, r.Vendor, r.Renderer)

	if r.Error != "" {
		t.Fatalf("FAIL: WebGL unavailable: %q", r.Error)
	}

	// FAIL if Mac-style vendor on a non-Mac platform.
	isLinux := strings.Contains(r.Platform, "Linux")
	macStyleVendor := r.UnmaskedVendor == "Intel Inc." || strings.HasPrefix(r.UnmaskedVendor, "Apple")
	macStyleRenderer := strings.Contains(r.UnmaskedRenderer, "Intel Iris") ||
		strings.Contains(r.UnmaskedRenderer, "OpenGL Engine") ||
		strings.HasPrefix(r.UnmaskedRenderer, "Apple")

	if isLinux && macStyleVendor {
		t.Errorf("FAIL: UNMASKED_VENDOR_WEBGL=%q on platform=%q — Mac-style vendor on Linux. "+
			"Real Linux Chrome reports Mesa/Google Inc./Khronos. amiunique scores this at 1.85%% similarity.",
			r.UnmaskedVendor, r.Platform)
	}
	if isLinux && macStyleRenderer {
		t.Errorf("FAIL: UNMASKED_RENDERER_WEBGL=%q on platform=%q — macOS-style renderer string on Linux. "+
			"amiunique scores this at 1.22%% similarity. Strong cross-fingerprint Google signal.",
			r.UnmaskedRenderer, r.Platform)
	}
	if !strings.HasPrefix(r.Version, "WebGL ") {
		t.Errorf("FAIL: unexpected WebGL VERSION=%q", r.Version)
	}
}
