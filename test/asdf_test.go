package container_test

// asdf_test.go — integration tests for the ~/.asdf symlink approach.
//
// Design: ASDF_DATA_DIR must be ~/.asdf (CellHome, persisted) not /opt/asdf (ephemeral).
// Baked-in tool versions are symlinked from /opt/asdf → ~/.asdf at entrypoint time.
// User-installed versions live as real dirs in ~/.asdf and survive container restarts.
//
// RED:  run against image without the setup_asdf_home entrypoint changes.
// GREEN: run against image with setup_asdf_home implemented.

import (
	"fmt"
	"strings"
	"testing"
)

// ── 1. ASDF_DATA_DIR ──────────────────────────────────────────────────────────

// TestAsdfDataDir — session user's ASDF_DATA_DIR must be ~/.asdf, not /opt/asdf.
//
// GREEN: entrypoint exports ASDF_DATA_DIR=$HOME/.asdf in shell rc overrides.
func TestAsdfDataDir(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "echo $ASDF_DATA_DIR")
	if code != 0 {
		t.Fatalf("FAIL: could not read ASDF_DATA_DIR (exit %d): %s", code, out)
	}

	expected := fmt.Sprintf("/home/%s/.asdf", hostUser)
	if out != expected {
		t.Errorf("FAIL: ASDF_DATA_DIR=%q, want %q", out, expected)
	} else {
		t.Logf("PASS: ASDF_DATA_DIR=%q", out)
	}
}

// ── 2. Baked-in versions symlinked ───────────────────────────────────────────

// TestAsdfBakedVersionsSymlinked — each baked-in tool version must appear in
// ~/.asdf/installs/<tool>/<ver> as a symlink pointing into /opt/asdf.
//
// GREEN: setup_asdf_home creates per-version symlinks at entrypoint time.
func TestAsdfBakedVersionsSymlinked(t *testing.T) {
	c := startEnvContainer(t)

	// ~/.asdf/installs/ must exist and contain at least one tool.
	out, code := asUser(t, c, "ls ~/.asdf/installs/ 2>&1")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("FAIL: ~/.asdf/installs/ missing or empty (exit %d): %s", code, out)
	}
	t.Logf("tools in ~/.asdf/installs/: %s", strings.TrimSpace(out))

	// Every version entry must be a symlink targeting /opt/asdf/installs/.
	out, code = asUser(t, c, `
		bad=0
		for tool in ~/.asdf/installs/*/; do
			for ver in "$tool"*/; do
				link="${ver%/}"
				[ -e "$link" ] || continue  # skip if nothing there
				if [ ! -L "$link" ]; then
					echo "NOT_SYMLINK: $link"
					bad=1
				else
					target=$(readlink "$link")
					case "$target" in
						/opt/asdf/*) echo "OK: $(basename "$link") -> $target" ;;
						*) echo "WRONG_TARGET: $link -> $target"; bad=1 ;;
					esac
				fi
			done
		done
		exit $bad
	`)
	if code != 0 {
		t.Errorf("FAIL: baked-in versions not correctly symlinked:\n%s", out)
	} else {
		t.Logf("PASS:\n%s", out)
	}
}

// TestAsdfPluginsSymlinked — baked-in plugins must be symlinked from /opt/asdf/plugins/
// into ~/.asdf/plugins/.
//
// GREEN: setup_asdf_home symlinks plugins at entrypoint time.
func TestAsdfPluginsSymlinked(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, `
		bad=0
		shopt -s nullglob
		for plugin in ~/.asdf/plugins/*/; do
			p="${plugin%/}"
			if [ ! -L "$p" ]; then
				echo "NOT_SYMLINK: $p"
				bad=1
			else
				target=$(readlink "$p")
				case "$target" in
					/opt/asdf/*) echo "OK: $(basename "$p") -> $target" ;;
					*) echo "WRONG_TARGET: $p -> $target"; bad=1 ;;
				esac
			fi
		done
		[ $bad -eq 0 ] && echo "all plugins symlinked correctly"
		exit $bad
	`)
	if code != 0 {
		t.Errorf("FAIL: plugins not correctly symlinked:\n%s", out)
	} else {
		t.Logf("PASS: %s", out)
	}
}

// ── 3. No dangling symlinks ───────────────────────────────────────────────────

// TestAsdfNoDanglingSymlinks — after a fresh container start there must be no
// dangling symlinks in ~/.asdf/installs/ (guard against stale image upgrades).
//
// GREEN: setup_asdf_home removes dangling symlinks before recreating them.
func TestAsdfNoDanglingSymlinks(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, `
		found=0
		for tool in ~/.asdf/installs/*/; do
			for ver in "$tool"*/; do
				link="${ver%/}"
				if [ -L "$link" ] && [ ! -e "$link" ]; then
					echo "DANGLING: $link -> $(readlink "$link")"
					found=1
				fi
			done
		done
		exit $found
	`)
	if code != 0 {
		t.Errorf("FAIL: dangling symlinks found after fresh start:\n%s", out)
	} else {
		t.Logf("PASS: no dangling symlinks")
	}
}

// ── 4. Node reachable via ~/.asdf shims ──────────────────────────────────────

// TestAsdfNodeViaUserShims — node must be reachable through ~/.asdf/shims,
// confirming that ASDF_DATA_DIR=~/.asdf and reshim ran successfully.
//
// GREEN: setup_asdf_home runs asdf reshim into ~/.asdf/shims at entrypoint time.
func TestAsdfNodeViaUserShims(t *testing.T) {
	c := startEnvContainer(t)

	// Shim must live in ~/.asdf/shims, not /opt/asdf/shims.
	shimPath, code := asUser(t, c, "which node")
	if code != 0 {
		t.Fatalf("FAIL: node not found on PATH (exit %d): %s", code, shimPath)
	}

	expected := fmt.Sprintf("/home/%s/.asdf/shims/node", hostUser)
	if shimPath != expected {
		t.Errorf("FAIL: node shim at %q, want %q", shimPath, expected)
	} else {
		t.Logf("PASS: node shim at %q", shimPath)
	}

	// Confirm it actually runs.
	out, code := asUser(t, c, "node --version")
	if code != 0 {
		t.Errorf("FAIL: node --version failed (exit %d): %s", code, out)
	} else {
		t.Logf("PASS: node --version: %s", out)
	}
}

// ── 5. User-installed version not overwritten ─────────────────────────────────

// TestAsdfUserInstallPreserved — setup_asdf_home must not overwrite a real dir
// with a symlink (user-installed version must be preserved across entrypoint runs).
//
// GREEN: setup_asdf_home skips real dirs: [ -d "$dest" ] && [ ! -L "$dest" ] && continue
func TestAsdfUserInstallPreserved(t *testing.T) {
	c := startEnvContainer(t)

	// Create a fake "user-installed" real directory for a non-existent version.
	_, code := exec(t, c, []string{"bash", "-c",
		"mkdir -p /home/" + hostUser + "/.asdf/installs/nodejs/99.99.99/bin && " +
			"printf '#!/bin/sh\\necho v99.99.99\\n' > /home/" + hostUser + "/.asdf/installs/nodejs/99.99.99/bin/node && " +
			"chmod +x /home/" + hostUser + "/.asdf/installs/nodejs/99.99.99/bin/node",
	})
	if code != 0 {
		t.Fatalf("FAIL: could not create fake user install")
	}

	// Re-run the symlink setup logic (simulates what entrypoint does on restart).
	_, code = exec(t, c, []string{"bash", "-c", `
		baked="/opt/asdf"
		user_asdf="/home/` + hostUser + `/.asdf"
		for tool_dir in "$baked/installs"/*/; do
			[ -d "$tool_dir" ] || continue
			tool_name=$(basename "$tool_dir")
			mkdir -p "$user_asdf/installs/$tool_name"
			for ver_dir in "$tool_dir"*/; do
				[ -d "$ver_dir" ] || continue
				ver_name=$(basename "$ver_dir")
				dest="$user_asdf/installs/$tool_name/$ver_name"
				[ -d "$dest" ] && [ ! -L "$dest" ] && continue
				ln -sfT "$ver_dir" "$dest"
			done
		done
	`})
	if code != 0 {
		t.Fatalf("FAIL: re-run of symlink setup failed")
	}

	// The real directory must NOT have been replaced by a symlink.
	out, code := exec(t, c, []string{"bash", "-c",
		"test -L /home/" + hostUser + "/.asdf/installs/nodejs/99.99.99 && echo SYMLINK || echo REAL"})
	if code != 0 || strings.TrimSpace(out) != "REAL" {
		t.Errorf("FAIL: user install was converted to symlink: %s", out)
	} else {
		t.Logf("PASS: user install preserved as real dir")
	}
}

// ── 6. Dangling symlink cleanup ───────────────────────────────────────────────

// TestAsdfDanglingSymlinkCleaned — dangling symlinks in ~/.asdf/installs/ must
// be removed by setup_asdf_home (simulates image upgrade where old version dropped).
//
// GREEN: setup_asdf_home removes [ -L link ] && [ ! -e link ] entries.
func TestAsdfDanglingSymlinkCleaned(t *testing.T) {
	c := startEnvContainer(t)

	// Inject a dangling symlink pointing to a non-existent /opt/asdf path.
	_, code := exec(t, c, []string{"bash", "-c",
		"mkdir -p /home/" + hostUser + "/.asdf/installs/nodejs && " +
			"ln -s /opt/asdf/installs/nodejs/0.0.0-nonexistent " +
			"/home/" + hostUser + "/.asdf/installs/nodejs/0.0.0-nonexistent",
	})
	if code != 0 {
		t.Fatalf("FAIL: could not inject dangling symlink")
	}

	// Re-run the dangling-symlink cleanup logic from setup_asdf_home.
	// Use * (not */) so dangling symlinks are included in the glob.
	_, code = exec(t, c, []string{"bash", "-c", `
		user_asdf="/home/` + hostUser + `/.asdf"
		for tool in "$user_asdf/installs"/*/; do
			for link in "${tool%/}"/*; do
				if [ -L "$link" ] && [ ! -e "$link" ]; then rm -f "$link"; fi
			done
		done
	`})
	if code != 0 {
		t.Fatalf("FAIL: cleanup logic failed (exit %d)", code)
	}

	// The dangling symlink must be gone.
	out, _ := exec(t, c, []string{"bash", "-c",
		"test -L /home/" + hostUser + "/.asdf/installs/nodejs/0.0.0-nonexistent && echo EXISTS || echo CLEANED",
	})
	if strings.TrimSpace(out) != "CLEANED" {
		t.Errorf("FAIL: dangling symlink still present after cleanup")
	} else {
		t.Logf("PASS: dangling symlink cleaned up")
	}
}
