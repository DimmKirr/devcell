// playwright_singleton_lock_test.go — CELL-62 actual root cause.
//
// When patchright Chromium SIGTRAPs or is SIGKILL'd mid-session, the three
// singleton-coordination files it created in its user-data-dir are left
// dangling:
//
//   SingletonLock   -> <hostname>-<pid>          (symlink target encodes PID)
//   SingletonCookie -> <random>-<unix-time>
//   SingletonSocket -> /home/<user>/tmp/.org.chromium.Chromium.<rand>/SingletonSocket
//
// The next launch through `mcp__playwright__browser_*` reads the stale
// SingletonLock from the host-mounted user-data-dir. In some Chromium
// versions / on Docker-for-Mac's fakeowner FUSE, the break-stale-lock path
// races or crashes — surfacing to the user as a SIGTRAP and "Target page,
// context or browser has been closed".
//
// The boot-time 22-chromium-singleton.sh fragment only sweeps at container
// startup, so a chromium that crashes between MCP calls poisons every
// subsequent launch within the same container generation. This test
// verifies the per-launch sweep wired into the patchright-mcp-cell wrapper:
// before exec'ing chrome, the wrapper invokes chromium-singleton-sweep on
// the user-data-dir, removing stale entries while preserving live owners.

package container_test

import (
	"strings"
	"testing"
)

// TestMcp_ChromiumSingletonSweep_HelperPresent_DIMM222 — basic wiring check:
// the chromium-singleton-sweep nix-store binary must be referenced (and thus
// reachable on the image's filesystem) from the patchright-mcp-cell wrapper.
// If a future refactor accidentally drops the call, this catches it before
// the per-behavior tests run.
func TestMcp_ChromiumSingletonSweep_HelperPresent_DIMM222(t *testing.T) {
	c := startEnvContainer(t)
	out, code := exec(t, c, []string{"sh", "-c",
		"cat $(command -v patchright-mcp-cell) | grep -c chromium-singleton-sweep"})
	if code != 0 {
		t.Fatalf("FAIL CELL-62: patchright-mcp-cell wrapper does not reference chromium-singleton-sweep (grep exit=%d, out=%q) — per-launch sweep not wired", code, out)
	}
	t.Logf("PASS: wrapper references chromium-singleton-sweep (%s matches)", strings.TrimSpace(out))
}

// TestMcp_ChromiumSingletonSweep_RemovesStaleTriple_DIMM222 — plant the
// triple as a stale set (dead PID), run the sweep directly, assert all
// three files are removed and the orphan /tmp/.org.chromium.Chromium.* dir
// referenced by the SingletonSocket symlink is also cleaned.
func TestMcp_ChromiumSingletonSweep_RemovesStaleTriple_DIMM222(t *testing.T) {
	c := startEnvContainer(t)

	setupScript := `set -e
SWEEP=$(grep -oE '/nix/store/[^ ]*chromium-singleton-sweep' $(command -v patchright-mcp-cell) | head -1)
echo "sweep_bin=$SWEEP"
[ -x "$SWEEP" ] || { echo "FATAL: sweep binary not executable: $SWEEP" >&2; exit 1; }

DIR=/tmp/dimm222-stale
ORPHAN=/tmp/.org.chromium.Chromium.STALE
mkdir -p "$DIR" "$ORPHAN"

# Pick a dead PID.
DEAD=999999
while kill -0 "$DEAD" 2>/dev/null; do DEAD=$((DEAD + 1)); done
HOSTNAME_VAL=$(cat /proc/sys/kernel/hostname)
ln -sfn "${HOSTNAME_VAL}-${DEAD}"             "$DIR/SingletonLock"
ln -sfn "1234567890123456789"                  "$DIR/SingletonCookie"
ln -sfn "${ORPHAN}/SingletonSocket"            "$DIR/SingletonSocket"
touch "${ORPHAN}/SingletonSocket"

echo "before-sweep:"
ls -la "$DIR"/Singleton* 2>&1
echo "orphan-before: $(ls -la $ORPHAN 2>&1)"

"$SWEEP" "$DIR" 2>&1 || true

echo "after-sweep:"
ls -la "$DIR"/Singleton* 2>&1 || echo "  (all removed)"
echo "orphan-after: $(ls -la $ORPHAN 2>&1 || echo '  (removed)')"
`
	out, code := exec(t, c, []string{"bash", "-c", setupScript})
	t.Logf("setup+sweep output:\n%s", out)
	if code != 0 {
		t.Fatalf("FAIL: setup script exit=%d", code)
	}

	// Each triple file must be gone, and the orphan dir must be removed.
	for _, f := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_, c2 := exec(t, c, []string{"test", "-h", "/tmp/dimm222-stale/" + f})
		if c2 == 0 {
			t.Errorf("FAIL CELL-62: /tmp/dimm222-stale/%s still exists after sweep — stale lock not removed", f)
		}
	}
	_, c2 := exec(t, c, []string{"test", "-d", "/tmp/.org.chromium.Chromium.STALE"})
	if c2 == 0 {
		t.Errorf("FAIL CELL-62: orphan /tmp/.org.chromium.Chromium.STALE/ still exists — orphan tmp dir not cleaned")
	}

	// The sweep must have logged the action to stderr.
	if !strings.Contains(out, "removed stale Singleton") {
		t.Errorf("FAIL CELL-62: expected 'removed stale Singleton' log line from sweep, got: %s", out)
	}
}

// TestMcp_ChromiumSingletonSweep_PreservesLiveChromeLock_DIMM222 — plant a
// lock whose recorded PID is our own (so kill -0 succeeds), then masquerade
// the comm by binding the test bash process as the "chrome" check target.
// Since the sweep reads /proc/<pid>/comm and our shell's comm is "bash"
// (not chrome), the sweep WILL remove it — that's the "recycled PID"
// branch. To validate the genuine-live-chrome case we need an actual
// chrome-named process. Spawn a sleep wrapper as "chrome" via exec -a.
//
// Without this test, a regression that removes the "comm matches chrome"
// guard would silently destroy live chromium state.
func TestMcp_ChromiumSingletonSweep_PreservesLiveChromeLock_DIMM222(t *testing.T) {
	c := startEnvContainer(t)

	setupScript := `set -e
SWEEP=$(grep -oE '/nix/store/[^ ]*chromium-singleton-sweep' $(command -v patchright-mcp-cell) | head -1)
[ -n "$SWEEP" ] || { echo "FATAL: sweep binary not referenced by patchright-mcp-cell wrapper" >&2; exit 1; }
[ -x "$SWEEP" ] || { echo "FATAL: sweep binary not executable: $SWEEP" >&2; exit 1; }
DIR=/tmp/dimm222-live
mkdir -p "$DIR"

# Spoof comm=chrome: the kernel sets /proc/<pid>/comm from the basename of
# the binary at exec time. Copy sleep to a path named "chrome" — root's
# home is on the overlay fs (executable), unlike /tmp which may be noexec.
SLEEP_BIN=$(command -v sleep)
[ -x "$SLEEP_BIN" ] || SLEEP_BIN=/bin/sleep
mkdir -p /root/dimm222-spoof
cp "$SLEEP_BIN" /root/dimm222-spoof/chrome
/root/dimm222-spoof/chrome 30 &
LIVE_PID=$!
for i in 1 2 3 4 5; do
  COMM=$(cat /proc/$LIVE_PID/comm 2>/dev/null || true)
  [ "$COMM" = "chrome" ] && break
  sleep 0.1
done
echo "live_pid=$LIVE_PID comm=$(cat /proc/$LIVE_PID/comm 2>/dev/null)"
[ "$(cat /proc/$LIVE_PID/comm 2>/dev/null)" = "chrome" ] || {
  echo "FATAL: failed to spoof comm=chrome (got: $(cat /proc/$LIVE_PID/comm 2>/dev/null))" >&2; exit 1; }

HOSTNAME_VAL=$(cat /proc/sys/kernel/hostname)
ln -sfn "${HOSTNAME_VAL}-${LIVE_PID}" "$DIR/SingletonLock"
ln -sfn "live-cookie-value"            "$DIR/SingletonCookie"
ln -sfn "/tmp/live-socket-target"      "$DIR/SingletonSocket"

"$SWEEP" "$DIR" 2>&1 || true

echo "after-sweep:"
ls -la "$DIR"/Singleton* 2>&1

# Clean up the masquerade.
kill "$LIVE_PID" 2>/dev/null || true
`
	out, code := exec(t, c, []string{"bash", "-c", setupScript})
	t.Logf("setup+sweep output:\n%s", out)
	if code != 0 {
		t.Fatalf("FAIL: setup script exit=%d", code)
	}

	// All three files must still exist — the sweep must NOT have touched
	// them because the recorded PID is alive AND its comm matches chrome*.
	for _, f := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_, c2 := exec(t, c, []string{"test", "-h", "/tmp/dimm222-live/" + f})
		if c2 != 0 {
			t.Errorf("FAIL CELL-62: /tmp/dimm222-live/%s was removed by sweep — live chrome lock destroyed (regression in comm-check)", f)
		}
	}
	if strings.Contains(out, "removed stale Singleton") {
		t.Errorf("FAIL CELL-62: sweep logged removal for a live chrome PID — comm-check guard is broken")
	}
}
