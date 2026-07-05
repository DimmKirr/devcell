package container_test

// stealth_l3_codec_test.go — CELL-20: H.264 codec support. Real Chrome
// reports `canPlayType("video/mp4; codecs=avc1.42E01E")` == "probably";
// upstream Chromium returns "". Sannysoft flags as VIDEO_CODECS WARN.

import (
	"context"
	"encoding/json"
	"testing"
)

const codecProbeServer = `#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HTML = (
    '<!DOCTYPE html><html><head><title>codec</title></head><body>'
    '<video id="v"></video><script>'
    '(async function(){'
    '  const v = document.getElementById("v");'
    '  const out = {};'
    '  out.h264_baseline = v.canPlayType("video/mp4; codecs=\\\"avc1.42E01E\\\"");'
    '  out.h264_main     = v.canPlayType("video/mp4; codecs=\\\"avc1.4d4015\\\"");'
    '  out.aac_lc        = v.canPlayType("audio/mp4; codecs=\\\"mp4a.40.2\\\"");'
    '  out.mp3           = v.canPlayType("audio/mpeg");'
    '  out.vp9_webm      = v.canPlayType("video/webm; codecs=\\\"vp9\\\"");'
    '  out.opus_webm     = v.canPlayType("audio/webm; codecs=\\\"opus\\\"");'
    '  if (window.MediaSource) {'
    '    out.ms_h264_baseline = MediaSource.isTypeSupported("video/mp4; codecs=\\\"avc1.42E01E\\\"");'
    '    out.ms_aac_lc       = MediaSource.isTypeSupported("audio/mp4; codecs=\\\"mp4a.40.2\\\"");'
    '  }'
    '  if (navigator.mediaCapabilities) {'
    '    try {'
    '      const r = await navigator.mediaCapabilities.decodingInfo({'
    '        type: "file",'
    '        video: { contentType: "video/mp4; codecs=\\\"avc1.42E01E\\\"", width:1280, height:720, bitrate:1500000, framerate:30 }'
    '      });'
    '      out.mc_h264 = { supported: r.supported, smooth: r.smooth, powerEfficient: r.powerEfficient };'
    '    } catch (e) { out.mc_h264_err = String(e); }'
    '  }'
    '  await fetch("/results", {method:"POST", headers:{"Content-Type":"application/json"}, body: JSON.stringify(out)});'
    '  document.title = "codec DONE";'
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
            with open('/tmp/codec-results.json','wb') as f: f.write(self.rfile.read(n))
            self.send_response(204); self.end_headers()
        else: self.send_error(404)

s = ThreadingHTTPServer(('127.0.0.1', 0), H)
with open('/tmp/codec-port.txt','w') as f: f.write(str(s.server_address[1]))
print('ready', flush=True)
s.serve_forever()
`

const codecProbeClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, time
CHROMIUM='/opt/devcell/.local/state/nix/profiles/profile/bin/chromium'
url='http://127.0.0.1:'+open('/tmp/codec-port.txt').read().strip()+'/probe'
env=dict(os.environ); env['PLAYWRIGHT_MCP_USER_DATA_DIR']='/tmp/pw-codec-l3'
p=subprocess.Popen(['patchright-mcp-cell','--headless','--browser','chromium','--executable-path',CHROMIUM],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open('/tmp/codec-mcp-stderr.log','w'), env=env)
def send(m): p.stdin.write((json.dumps(m)+'\n').encode()); p.stdin.flush()
def recv(): return json.loads(p.stdout.readline())
try:
    send({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'codec','version':'0'}}}); recv()
    send({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'browser_navigate','arguments':{'url':url}}})
    r=recv()
    if 'error' in r: print('nav err',r,file=sys.stderr); sys.exit(2)
    d=time.time()+45
    while time.time()<d:
        if os.path.exists('/tmp/codec-results.json') and os.path.getsize('/tmp/codec-results.json')>10:
            print('DONE',flush=True); sys.exit(0)
        time.sleep(0.5)
    sys.exit(3)
finally:
    p.terminate()
    try: p.wait(timeout=5)
    except Exception: p.kill()
`

// TestStealth_L3_H264CodecSupport — CELL-20. Expected RED: H.264 and
// AAC return "" (unsupported) on Chromium-without-proprietary-codecs.
func TestStealth_L3_H264CodecSupport(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "codec-l3",
		"USER_WORKING_DIR": "/tmp/codec-l3-wd",
	})
	if _, code := exec(t, c, []string{"sh", "-c", "command -v patchright-mcp-cell"}); code != 0 {
		t.Skip("patchright-mcp-cell not on PATH")
	}

	ctx := context.Background()
	if err := c.CopyToContainer(ctx, []byte(codecProbeServer), "/tmp/codec-probe-server.py", 0o755); err != nil {
		t.Fatalf("copy server: %v", err)
	}
	if err := c.CopyToContainer(ctx, []byte(codecProbeClient), "/tmp/codec-probe-client.py", 0o755); err != nil {
		t.Fatalf("copy client: %v", err)
	}

	exec(t, c, []string{"sh", "-c", "rm -f /tmp/codec-*.json /tmp/codec-port.txt /tmp/codec-mcp-stderr.log /tmp/codec-server.log"})
	exec(t, c, []string{"bash", "-c", "nohup python3 /tmp/codec-probe-server.py > /tmp/codec-server.log 2>&1 &"})
	if _, code := exec(t, c, []string{"bash", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do [ -f /tmp/codec-port.txt ] && exit 0; sleep 0.5; done; exit 1",
	}); code != 0 {
		t.Fatal("probe server start")
	}

	out, code := exec(t, c, []string{"python3", "/tmp/codec-probe-client.py"})
	t.Logf("MCP:\n%s", out)
	if code != 0 {
		stderr, _ := exec(t, c, []string{"sh", "-c", "tail -100 /tmp/codec-mcp-stderr.log"})
		t.Fatalf("MCP exit %d\n%s", code, stderr)
	}

	resultsRaw, code := exec(t, c, []string{"cat", "/tmp/codec-results.json"})
	if code != 0 {
		t.Fatal("/tmp/codec-results.json missing")
	}
	var r struct {
		H264Baseline   string `json:"h264_baseline"`
		H264Main       string `json:"h264_main"`
		AACLC          string `json:"aac_lc"`
		MP3            string `json:"mp3"`
		VP9Webm        string `json:"vp9_webm"`
		OpusWebm       string `json:"opus_webm"`
		MSH264Baseline bool   `json:"ms_h264_baseline"`
		MSAACLC        bool   `json:"ms_aac_lc"`
		MCH264         *struct {
			Supported bool `json:"supported"`
		} `json:"mc_h264"`
	}
	if err := json.Unmarshal([]byte(resultsRaw), &r); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, resultsRaw)
	}
	t.Logf("probe: h264=%q h264_main=%q aac=%q mp3=%q vp9=%q opus=%q ms_h264=%v ms_aac=%v",
		r.H264Baseline, r.H264Main, r.AACLC, r.MP3, r.VP9Webm, r.OpusWebm, r.MSH264Baseline, r.MSAACLC)

	if r.H264Baseline != "probably" {
		t.Errorf("FAIL: canPlayType(H.264 baseline avc1.42E01E)=%q, want %q. "+
			"Chromium-without-proprietary-codecs returns \"\" — claims Chrome UA but can't actually decode H.264. "+
			"Strong sannysoft / creepjs fingerprint (mimes 6/12).",
			r.H264Baseline, "probably")
	}
	if r.H264Main != "probably" {
		t.Errorf("FAIL: canPlayType(H.264 main avc1.4d4015)=%q, want %q",
			r.H264Main, "probably")
	}
	if r.AACLC != "probably" {
		t.Errorf("FAIL: canPlayType(AAC-LC mp4a.40.2)=%q, want %q (AAC ships with H.264)",
			r.AACLC, "probably")
	}
	if !r.MSH264Baseline {
		t.Errorf("FAIL: MediaSource.isTypeSupported(H.264 baseline)=false, want true")
	}
	if !r.MSAACLC {
		t.Errorf("FAIL: MediaSource.isTypeSupported(AAC-LC)=false, want true")
	}
	if r.MCH264 != nil && !r.MCH264.Supported {
		t.Errorf("FAIL: mediaCapabilities.decodingInfo(H.264).supported=false, want true")
	}
	// Sanity: open codecs MUST work (else something is very wrong).
	if r.VP9Webm != "probably" {
		t.Errorf("SANITY FAIL: VP9 canPlayType=%q (open codec — should always work)", r.VP9Webm)
	}
	if r.OpusWebm != "probably" {
		t.Errorf("SANITY FAIL: Opus canPlayType=%q", r.OpusWebm)
	}
}
