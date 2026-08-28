// wireguard_test.go — WireGuard VPN tunnel integration tests (CELL-451)
//
// Happy path: starts a container with WireGuard, curls ipconfig.io/json,
// verifies country_iso=PT. Requires WG_PRIVATE_KEY env var (ProtonVPN)
// and an image with wg-quick installed (wireguard module enabled).
//
// Unhappy paths: missing key (clear error), wireguard disabled (no PT exit).

package container_test

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"strings"
	"testing"
)

// protonPTConfig is a ProtonVPN Portugal WireGuard config (sans PrivateKey).
// PostUp loads the key from /run/secrets/wg-private-key at runtime.
const protonPTConfig = `[Interface]
Address = 10.2.0.2/32
DNS = 10.2.0.1
PostUp = wg set %i private-key /run/secrets/wg-private-key

[Peer]
PublicKey = PLACEHOLDER_MUST_BE_SET_BY_TOML
Endpoint = 79.127.131.222:51820
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`

// wgTestConfig returns the ProtonVPN PT config with the real public key.
func wgTestConfig() string {
	pubKey := os.Getenv("WG_PT_PUBLIC_KEY")
	if pubKey == "" {
		pubKey = "fkBdrgo6NaOI9ICRd+i2mDbieKUzEXkj4vX3ItZ+5lM="
	}
	return strings.Replace(protonPTConfig, "PLACEHOLDER_MUST_BE_SET_BY_TOML", pubKey, 1)
}

type ipConfigResponse struct {
	IP         string `json:"ip"`
	CountryISO string `json:"country_iso"`
	Country    string `json:"country"`
}

// wgBootScript returns a bash script that:
// 1. Creates /run/secrets tmpfs (normally added by runner --tmpfs)
// 2. Injects the WireGuard entrypoint fragment
// 3. Writes the WG config file
// 4. Execs the entrypoint with innerCmd
func wgBootScript(wgConf, innerCmd string) string {
	return `set -e
mkdir -p /run/secrets && chmod 700 /run/secrets

mkdir -p /etc/devcell/entrypoint.d
cat > /etc/devcell/entrypoint.d/26-wireguard.sh << 'WGFRAG'
#!/usr/bin/env bash
[ "${DEVCELL_WG_ENABLED:-}" = "1" ] || return 0
command -v notify >/dev/null 2>&1 && notify wireguard.starting

WG_DIR="/home/$HOST_USER/.devcell/$DEVCELL_CELL_NAME/.wg"
if [ ! -d "$WG_DIR" ] || [ -z "$(ls -A "$WG_DIR"/*.conf 2>/dev/null)" ]; then
  log "wireguard: no .conf files in $WG_DIR, skipping"
  command -v notify >/dev/null 2>&1 && notify wireguard.ready
  return 0
fi

if [ -z "${WG_PRIVATE_KEY:-}" ]; then
  echo "wireguard: WG_PRIVATE_KEY must be set when wireguard is enabled" >&2
  exit 1
fi

echo "$WG_PRIVATE_KEY" > /run/secrets/wg-private-key
chmod 600 /run/secrets/wg-private-key

if [ -n "${WG_PRESHARED_KEY:-}" ]; then
  echo "$WG_PRESHARED_KEY" > /run/secrets/wg-preshared-key
  chmod 600 /run/secrets/wg-preshared-key
fi

WQ_WRAPPER=$(readlink -f "$(command -v wg-quick)")
WQ_UNWRAPPED=$(grep "exec -a" "$WQ_WRAPPER" | grep -oP '/nix/store/[^"]+' | head -1)
cp "$WQ_UNWRAPPED" /tmp/wg-quick-patched
sed -i 's|cmd sysctl -q net.ipv4.conf.all.src_valid_mark=1|true|' /tmp/wg-quick-patched
chmod +x /tmp/wg-quick-patched
sed "s|$WQ_UNWRAPPED|/tmp/wg-quick-patched|" "$WQ_WRAPPER" > /tmp/wg-quick
chmod +x /tmp/wg-quick

for conf in "$WG_DIR"/*.conf; do
  [ -f "$conf" ] || continue
  name=$(basename "$conf" .conf)

  dns=$(grep -oP '^\s*DNS\s*=\s*\K\S+' "$conf")
  conf_nodns="/tmp/${name}.conf"
  grep -v '^\s*DNS\s*=' "$conf" > "$conf_nodns"

  /tmp/wg-quick up "$conf_nodns" 2>&1 | while IFS= read -r line; do log "wireguard[$name]: $line"; done
  rm -f "$conf_nodns"

  if [ -n "$dns" ]; then
    echo "nameserver $dns" > /etc/resolv.conf
    log "wireguard[$name]: DNS set to $dns"
  fi

  log "wireguard: $name up"
done

command -v notify >/dev/null 2>&1 && notify wireguard.ready
WGFRAG
chmod +x /etc/devcell/entrypoint.d/26-wireguard.sh

mkdir -p /home/$HOST_USER/.devcell/$DEVCELL_CELL_NAME/.wg
cat > /home/$HOST_USER/.devcell/$DEVCELL_CELL_NAME/.wg/wg0.conf << 'WGCONF'
` + wgConf + `
WGCONF
chmod 600 /home/$HOST_USER/.devcell/$DEVCELL_CELL_NAME/.wg/wg0.conf

exec /opt/devcell/.local/bin/entrypoint.sh ` + innerCmd
}

// wgBaseArgs returns docker run args with thin volume if needed.
func wgBaseArgs(img string, env map[string]string, extraArgs ...string) []string {
	args := []string{"run", "--rm", "--user", "0"}
	if isThinVariant() {
		args = append(args, "-v", thinVolumeName()+":/nix")
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, extraArgs...)
	args = append(args, img)
	return args
}

// requireWgQuick skips the test if the image doesn't have wg-quick installed.
func requireWgQuick(t *testing.T, img string) {
	t.Helper()
	args := []string{"run", "--rm"}
	if isThinVariant() {
		args = append(args, "-v", thinVolumeName()+":/nix")
	}
	args = append(args, img, "bash", "-lc", "command -v wg-quick")
	out, err := osexec.Command("docker", args...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "wg-quick") {
		t.Skip("wg-quick not installed in image: rebuild with wireguard module enabled (`cell build --thin`)")
	}
}

// TestWireguard_PTExitIP verifies WireGuard tunnel exits with a Portuguese IP.
// Requires: WG_PRIVATE_KEY env var, image with wg-quick installed.
func TestWireguard_PTExitIP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	privateKey := os.Getenv("WG_PRIVATE_KEY")
	if privateKey == "" {
		t.Skip("WG_PRIVATE_KEY not set: set it to run WireGuard integration tests")
	}

	img := image()
	requireWgQuick(t, img)

	script := wgBootScript(wgTestConfig(),
		`bash -c "sleep 3 && curl -s --max-time 15 https://ipconfig.io/json"`)

	args := wgBaseArgs(img, map[string]string{
		"HOST_USER":          "testuser",
		"APP_NAME":           "wgtest",
		"DEVCELL_WG_ENABLED": "1",
		"DEVCELL_CELL_NAME":  "main",
		"WG_PRIVATE_KEY":     privateKey,
		"DEVCELL_DEBUG":      "true",
	},
		"--cap-add=NET_ADMIN",
		"--device=/dev/net/tun",
		"--sysctl", "net.ipv4.conf.all.src_valid_mark=1",
		"--entrypoint", "bash",
	)
	args = append(args, "-c", script)

	out, err := osexec.Command("docker", args...).CombinedOutput()
	t.Logf("output:\n%s", string(out))
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	jsonStr := extractJSON(string(out))
	if jsonStr == "" {
		t.Fatalf("no JSON found in output:\n%s", out)
	}

	var resp ipConfigResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		t.Fatalf("parse ipconfig.io response: %v\nraw: %s", err, jsonStr)
	}

	t.Logf("exit IP: %s, country: %s (%s)", resp.IP, resp.Country, resp.CountryISO)
	if resp.CountryISO != "PT" {
		t.Fatalf("expected country_iso=PT, got %q (IP: %s)", resp.CountryISO, resp.IP)
	}
}

// TestWireguard_MissingKey verifies the entrypoint fails with a clear error
// when DEVCELL_WG_ENABLED=1 but WG_PRIVATE_KEY is not set.
func TestWireguard_MissingKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	img := image()
	script := wgBootScript(wgTestConfig(), `echo should-not-reach`)

	args := wgBaseArgs(img, map[string]string{
		"HOST_USER":          "testuser",
		"APP_NAME":           "wgtest",
		"DEVCELL_WG_ENABLED": "1",
		"DEVCELL_CELL_NAME":  "main",
		// WG_PRIVATE_KEY intentionally not set
	},
		"--cap-add=NET_ADMIN",
		"--device=/dev/net/tun",
		"--entrypoint", "bash",
	)
	args = append(args, "-c", script)

	out, err := osexec.Command("docker", args...).CombinedOutput()

	output := string(out)
	t.Logf("output:\n%s", output)

	if err == nil && strings.Contains(output, "should-not-reach") {
		t.Fatal("container should have failed due to missing WG_PRIVATE_KEY, but reached echo")
	}

	if !strings.Contains(output, "WG_PRIVATE_KEY") {
		t.Fatal("error output should mention WG_PRIVATE_KEY")
	}
}

// TestWireguard_Disabled verifies that without wireguard, the container
// does NOT exit through a PT IP.
func TestWireguard_Disabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	img := image()

	args := wgBaseArgs(img, map[string]string{
		"HOST_USER": "testuser",
		"APP_NAME":  "wgtest",
	})
	args = append(args, "bash", "-c", "curl -s --max-time 10 https://ipconfig.io/json || echo '{}'")

	out, err := osexec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\noutput: %s", err, out)
	}

	jsonStr := extractJSON(string(out))
	if jsonStr == "" {
		t.Log("no JSON from ipconfig.io (possibly no network): PASS by default")
		return
	}

	var resp ipConfigResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		t.Logf("parse error (not JSON): %v", err)
		return
	}

	t.Logf("exit IP: %s, country: %s (%s)", resp.IP, resp.Country, resp.CountryISO)
	if resp.CountryISO == "PT" {
		t.Fatal("without wireguard, exit IP should NOT be PT")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// extractJSON finds the first JSON object in a string (handles entrypoint
// log noise before the curl output).
func extractJSON(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return ""
}
