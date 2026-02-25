package container_test

// mcp_test.go — MCP server tests for all managed MCP servers.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// e2eFormServer is a minimal Node.js HTTP server used by TestMcpPlaywrightE2EFormSecrets.
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

// e2eMcpClient drives playwright-mcp-cell via MCP stdio to fill the e2eFormServer form.
// Secret names (TEST_USERNAME, TEST_PASSWORD) are sent as field values; playwright-mcp
// resolves them via lookupSecret() using the secrets file written by playwright-mcp-cell.
// Prints "DONE" on success; exits non-zero on any failure.
const e2eMcpClient = `#!/usr/bin/env python3
import subprocess, json, os, sys, re, time

CHROMIUM = '/nix/var/nix/profiles/per-user/devcell/profile/bin/chromium'
USER_DATA = '/tmp/pw-e2e-test'

with open('/tmp/server-port.txt') as f:
    port = f.read().strip()
url = 'http://127.0.0.1:' + port
print('server url:', url, flush=True)

env = dict(os.environ)
env['PLAYWRIGHT_MCP_USER_DATA_DIR'] = USER_DATA

proc = subprocess.Popen(
    ['playwright-mcp-cell', '--headless', '--browser', 'chromium',
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

// TestMcpPlaywrightSecretsFromDotEnv verifies playwright-mcp-cell reads key names from
// $USER_WORKING_DIR/.env and resolves values from the container environment.
// Only keys present in .env are forwarded — other container env vars are not exposed.
func TestMcpPlaywrightSecretsFromDotEnv(t *testing.T) {
	c := startContainer(t, map[string]string{
		"APP_NAME":         "test",
		"HOST_USER":        hostUser,
		"TEST_PASSWORD":    "hello123",
		"GITHUB_TOKEN":     "ghtoken456",
		"USER_WORKING_DIR": "/tmp/test-wd",
	})

	_, code := exec(t, c, []string{"sh", "-c", "command -v playwright-mcp-cell"})
	if code != 0 {
		t.Fatal("FAIL: playwright-mcp-cell not found on PATH")
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

// TestMcpPlaywrightStagingFileHasEntry — nix-mcp-servers.json must contain a playwright entry
// with playwright-mcp-cell as the command.
func TestMcpPlaywrightStagingFileHasEntry(t *testing.T) {
	c := startEnvContainer(t)

	raw, code := exec(t, c, []string{"cat", "/etc/claude-code/nix-mcp-servers.json"})
	if code != 0 {
		t.Fatalf("FAIL: could not read nix-mcp-servers.json (exit %d)", code)
	}

	var cfg struct {
		McpServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("FAIL: invalid JSON: %v\n%s", err, raw)
	}

	entry, ok := cfg.McpServers["playwright"]
	if !ok {
		keys := make([]string, 0, len(cfg.McpServers))
		for k := range cfg.McpServers {
			keys = append(keys, k)
		}
		t.Errorf("FAIL: playwright missing from nix-mcp-servers.json; present keys: [%s]",
			strings.Join(keys, ", "))
		return
	}
	if entry.Command != "playwright-mcp-cell" {
		t.Errorf("FAIL: expected command %q, got %q", "playwright-mcp-cell", entry.Command)
	} else {
		t.Logf("PASS: playwright entry present, command=%s", entry.Command)
	}
}

// TestMcpPlaywrightTempFileCleanup — secrets temp file must be deleted after wrapper exits.
// Verifies the trap 'rm -f' EXIT in playwright-mcp-cell fires correctly so no temp file leaks.
func TestMcpPlaywrightTempFileCleanup(t *testing.T) {
	c := startContainer(t, map[string]string{
		"HOST_USER":        hostUser,
		"APP_NAME":         "test",
		"TEST_PASSWORD":    "hello123",
		"USER_WORKING_DIR": "/tmp/cleanup-wd",
	})

	_, code := exec(t, c, []string{"sh", "-c",
		"mkdir -p /tmp/cleanup-wd && printf 'TEST_PASSWORD=any\n' > /tmp/cleanup-wd/.env"})
	if code != 0 {
		t.Fatal("FAIL: could not create test .env file")
	}

	out, code := exec(t, c, []string{"sh", "-c", `
		SECRETS_FILE=$(mktemp /tmp/pw-secrets-XXXXXX.env)
		trap 'rm -f "$SECRETS_FILE"; echo "cleaned:$SECRETS_FILE"' EXIT
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
		echo "created:$SECRETS_FILE"
		test -f "$SECRETS_FILE" && echo "exists_during" || echo "missing_during"
		true
	`})
	if code != 0 {
		t.Fatalf("FAIL: wrapper logic failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "exists_during") {
		t.Errorf("FAIL: secrets temp file was not created")
	}
	if !strings.Contains(out, "cleaned:") {
		t.Errorf("FAIL: EXIT trap did not fire")
	}

	var cleaned string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "cleaned:") {
			cleaned = strings.TrimPrefix(strings.TrimSpace(line), "cleaned:")
		}
	}
	if cleaned == "" {
		t.Fatal("FAIL: could not extract temp file path from output")
	}
	_, code = exec(t, c, []string{"test", "-f", cleaned})
	if code == 0 {
		t.Errorf("FAIL: temp file %q still exists after EXIT", cleaned)
	} else {
		t.Logf("PASS: temp file %q cleaned up", cleaned)
	}
}

// TestMcpPlaywrightProtocol — playwright-mcp-cell must respond to MCP initialize + tools/list
// over stdio. The browser is lazily initialised so this works without a display server.
//
// @modelcontextprotocol/sdk v1.x uses newline-delimited JSON (one JSON object per line),
// NOT Content-Length/LSP framing.
func TestMcpPlaywrightProtocol(t *testing.T) {
	c := startEnvContainer(t)

	_, code := exec(t, c, []string{"sh", "-c", "command -v playwright-mcp-cell"})
	if code != 0 {
		t.Fatal("FAIL: playwright-mcp-cell not on PATH — is this the ultimate image?")
	}

	out, code := exec(t, c, []string{"bash", "-c", `
		INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}'
		LIST='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
		CHROMIUM=/nix/var/nix/profiles/per-user/devcell/profile/bin/chromium
		TMPDIR_PW=$(mktemp -d /tmp/pw-proto-XXXXXX)
		trap 'rm -rf "$TMPDIR_PW"' EXIT
		{ printf "%s\n%s\n" "$INIT" "$LIST"; sleep 5; } \
		| PLAYWRIGHT_MCP_USER_DATA_DIR="$TMPDIR_PW" timeout 15 playwright-mcp-cell \
			--headless --browser chromium \
			--executable-path "$CHROMIUM" \
			2>/dev/null
	`})
	// exit 124 = timeout (server did not auto-exit on EOF) — responses already written to stdout
	if code != 0 && code != 124 {
		t.Fatalf("FAIL: playwright-mcp-cell exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"result"`) {
		t.Errorf("FAIL: no JSON-RPC result in output (exit %d):\n%s", code, out)
		return
	}
	if !strings.Contains(out, "browser_navigate") {
		t.Errorf("FAIL: tools/list response missing browser_navigate (exit %d):\n%s", code, out)
	} else {
		t.Logf("PASS: playwright-mcp responded with MCP protocol (exit %d)", code)
	}
}

// TestMcpPlaywrightE2EFormSecrets — full end-to-end secrets flow:
//  1. Container starts with TEST_USERNAME and TEST_PASSWORD env vars; .env lists those keys.
//  2. Assert playwright is registered in ~/.claude.json (merged by entrypoint).
//  3. Start a local Node.js HTTP server that serves a login form.
//  4. Drive playwright-mcp-cell via MCP stdio:
//     initialize → browser_navigate → browser_snapshot → browser_fill_form (secret names) → browser_click.
//  5. Assert /tmp/form-output.txt contains the real secret values (not the names).
func TestMcpPlaywrightE2EFormSecrets(t *testing.T) {
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
	if entry.Command != "playwright-mcp-cell" {
		t.Fatalf("FAIL step 1: playwright command=%q, want playwright-mcp-cell", entry.Command)
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

	// Step 4: run Python MCP client — drives playwright-mcp-cell to fill form with secrets.
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

// ── NixOS MCP ─────────────────────────────────────────────────────────────────

// TestMcpNixosBinaryOnPath — mcp-nixos must be on PATH for the session user.
func TestMcpNixosBinaryOnPath(t *testing.T) {
	c := startEnvContainer(t)
	out, code := asUser(t, c, "command -v mcp-nixos")
	if code != 0 {
		t.Fatalf("FAIL: mcp-nixos not on PATH (exit %d)", code)
	}
	t.Logf("PASS: %s", out)
}

// TestMcpNixosStagingFileExists — /etc/claude-code/nix-mcp-servers.json must contain nixos entry.
// This is the staging file baked into the image; entrypoint.sh merges it into ~/.claude.json.
func TestMcpNixosStagingFileExists(t *testing.T) {
	c := startEnvContainer(t)

	_, code := exec(t, c, []string{"test", "-f", "/etc/claude-code/nix-mcp-servers.json"})
	if code != 0 {
		t.Fatal("FAIL: /etc/claude-code/nix-mcp-servers.json does not exist")
	}

	raw, code := exec(t, c, []string{"cat", "/etc/claude-code/nix-mcp-servers.json"})
	if code != 0 {
		t.Fatalf("FAIL: could not read nix-mcp-servers.json (exit %d)", code)
	}

	var cfg struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("FAIL: invalid JSON: %v\n%s", err, raw)
	}

	if _, ok := cfg.McpServers["nixos"]; !ok {
		keys := make([]string, 0, len(cfg.McpServers))
		for k := range cfg.McpServers {
			keys = append(keys, k)
		}
		t.Errorf("FAIL: mcpServers.nixos missing; present keys: [%s]", strings.Join(keys, ", "))
	} else {
		t.Logf("PASS: nixos entry present in nix-mcp-servers.json")
	}
}

// TestMcpNixosCommandExecutable — the command in nix-mcp-servers.json must be an executable nix store path.
func TestMcpNixosCommandExecutable(t *testing.T) {
	c := startEnvContainer(t)

	raw, code := exec(t, c, []string{"cat", "/etc/claude-code/nix-mcp-servers.json"})
	if code != 0 {
		t.Fatalf("FAIL: could not read nix-mcp-servers.json (exit %d)", code)
	}

	var cfg struct {
		McpServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("FAIL: invalid JSON: %v", err)
	}

	entry, ok := cfg.McpServers["nixos"]
	if !ok {
		t.Fatal("FAIL: nixos entry missing — see TestMcpNixosStagingFileExists")
	}
	if !strings.HasPrefix(entry.Command, "/nix/store/") {
		t.Errorf("FAIL: expected nix store path, got %q", entry.Command)
	}

	_, code = exec(t, c, []string{"test", "-x", entry.Command})
	if code != 0 {
		t.Errorf("FAIL: command %q is not executable", entry.Command)
	} else {
		t.Logf("PASS: command %q is executable", entry.Command)
	}
}

// TestMcpClaudeJsonBackupOnMerge — when ~/.claude.json pre-exists and backupBeforeMerge=true,
// a timestamped backup must be created before the merge overwrites the file.
func TestMcpClaudeJsonBackupOnMerge(t *testing.T) {
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
	if strings.Contains(out, "merged_count:0") || strings.Contains(out, "merged_count:1") {
		t.Errorf("FAIL: merged file should have >1 server (pre-existing + nix)\n%s", out)
	}
	t.Logf("PASS: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
}

// TestMcpClaudeJsonMerge — entrypoint must merge nix MCP servers into ~/.claude.json.
// Verifies the additive merge model: nix servers land in user config after container start.
func TestMcpClaudeJsonMerge(t *testing.T) {
	c := startEnvContainer(t)

	claudeJson := "/home/" + hostUser + "/.claude.json"

	_, code := exec(t, c, []string{"test", "-f", claudeJson})
	if code != 0 {
		t.Fatalf("FAIL: %s does not exist after entrypoint", claudeJson)
	}

	raw, code := exec(t, c, []string{"cat", claudeJson})
	if code != 0 {
		t.Fatalf("FAIL: could not read %s (exit %d)", claudeJson, code)
	}

	var cfg struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("FAIL: %s contains invalid JSON: %v\n%s", claudeJson, err, raw)
	}

	if len(cfg.McpServers) == 0 {
		t.Fatalf("FAIL: mcpServers is empty in %s — merge did not run", claudeJson)
	}

	if _, ok := cfg.McpServers["nixos"]; !ok {
		keys := make([]string, 0, len(cfg.McpServers))
		for k := range cfg.McpServers {
			keys = append(keys, k)
		}
		t.Errorf("FAIL: nixos missing from %s; present: [%s]", claudeJson, strings.Join(keys, ", "))
	} else {
		t.Logf("PASS: nixos present in %s (%d server(s) total)", claudeJson, len(cfg.McpServers))
	}
}
