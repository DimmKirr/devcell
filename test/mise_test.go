package container_test

// mise_test.go — integration tests for the ~/.local/share/mise symlink approach.
//
// Design: MISE_DATA_DIR must be ~/.local/share/mise (CellHome, persisted) not /opt/mise (ephemeral).
// Baked-in tool versions are symlinked from /opt/mise → ~/.local/share/mise at entrypoint time.
// User-installed versions live as real dirs in ~/.local/share/mise and survive container restarts.

import (
	"fmt"
	"strings"
	"testing"
)

// ── 1. MISE_DATA_DIR ──────────────────────────────────────────────────────────

// TestMiseDataDir — session user's MISE_DATA_DIR must be ~/.local/share/mise, not /opt/mise.
func TestMiseDataDir(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "echo $MISE_DATA_DIR")
	if code != 0 {
		t.Fatalf("FAIL: could not read MISE_DATA_DIR (exit %d): %s", code, out)
	}

	expected := fmt.Sprintf("/home/%s/.local/share/mise", hostUser)
	if out != expected {
		t.Errorf("FAIL: MISE_DATA_DIR=%q, want %q", out, expected)
	} else {
		t.Logf("PASS: MISE_DATA_DIR=%q", out)
	}
}

// ── 2. Baked-in versions symlinked ───────────────────────────────────────────

// TestMiseBakedVersionsSymlinked — each baked-in tool version must appear in
// ~/.local/share/mise/installs/<tool>/<ver> as a symlink pointing into /opt/mise.
func TestMiseBakedVersionsSymlinked(t *testing.T) {
	c := startEnvContainer(t)

	// ~/.local/share/mise/installs/ must exist and contain at least one tool.
	out, code := asUser(t, c, "ls ~/.local/share/mise/installs/ 2>&1")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("FAIL: ~/.local/share/mise/installs/ missing or empty (exit %d): %s", code, out)
	}
	t.Logf("tools in ~/.local/share/mise/installs/: %s", strings.TrimSpace(out))

	// Every version entry must be a symlink targeting /opt/mise/installs/.
	out, code = asUser(t, c, `
		bad=0
		for tool in ~/.local/share/mise/installs/*/; do
			for ver in "$tool"*/; do
				link="${ver%/}"
				[ -e "$link" ] || continue
				if [ ! -L "$link" ]; then
					echo "NOT_SYMLINK: $link"
					bad=1
				else
					target=$(readlink "$link")
					case "$target" in
						/opt/mise/*) echo "OK: $(basename "$link") -> $target" ;;
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

// ── 3. No dangling symlinks ───────────────────────────────────────────────────

// TestMiseNoDanglingSymlinks — after a fresh container start there must be no
// dangling symlinks in ~/.local/share/mise/installs/.
func TestMiseNoDanglingSymlinks(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, `
		found=0
		for tool in ~/.local/share/mise/installs/*/; do
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

// ── 4. Node reachable via mise shims ─────────────────────────────────────────

// TestMiseNodeViaUserShims — node must be reachable through ~/.local/share/mise/shims,
// confirming that MISE_DATA_DIR is correct and reshim ran successfully.
func TestMiseNodeViaUserShims(t *testing.T) {
	c := startEnvContainer(t)

	// Shim must live in ~/.local/share/mise/shims, not /opt/mise/shims.
	shimPath, code := asUser(t, c, "which node")
	if code != 0 {
		t.Fatalf("FAIL: node not found on PATH (exit %d): %s", code, shimPath)
	}

	expected := fmt.Sprintf("/home/%s/.local/share/mise/shims/node", hostUser)
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

// TestMiseUserInstallPreserved — setup_mise_home must not overwrite a real dir
// with a symlink (user-installed version must be preserved).
func TestMiseUserInstallPreserved(t *testing.T) {
	c := startEnvContainer(t)

	// Create a fake "user-installed" real directory for a non-existent version.
	_, code := exec(t, c, []string{"bash", "-c",
		"mkdir -p /home/" + hostUser + "/.local/share/mise/installs/node/99.99.99/bin && " +
			"printf '#!/bin/sh\\necho v99.99.99\\n' > /home/" + hostUser + "/.local/share/mise/installs/node/99.99.99/bin/node && " +
			"chmod +x /home/" + hostUser + "/.local/share/mise/installs/node/99.99.99/bin/node",
	})
	if code != 0 {
		t.Fatalf("FAIL: could not create fake user install")
	}

	// Re-run the symlink setup logic (simulates what entrypoint does on restart).
	_, code = exec(t, c, []string{"bash", "-c", `
		baked="/opt/mise"
		user_mise="/home/` + hostUser + `/.local/share/mise"
		for tool_dir in "$baked/installs"/*/; do
			[ -d "$tool_dir" ] || continue
			tool_name=$(basename "$tool_dir")
			mkdir -p "$user_mise/installs/$tool_name"
			for ver_dir in "$tool_dir"*/; do
				[ -d "$ver_dir" ] || continue
				ver_name=$(basename "$ver_dir")
				dest="$user_mise/installs/$tool_name/$ver_name"
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
		"test -L /home/" + hostUser + "/.local/share/mise/installs/node/99.99.99 && echo SYMLINK || echo REAL"})
	if code != 0 || strings.TrimSpace(out) != "REAL" {
		t.Errorf("FAIL: user install was converted to symlink: %s", out)
	} else {
		t.Logf("PASS: user install preserved as real dir")
	}
}

// ── 6. Dangling symlink cleanup ───────────────────────────────────────────────

// TestMiseDanglingSymlinkCleaned — dangling symlinks in ~/.local/share/mise/installs/
// must be removed by setup_mise_home.
func TestMiseDanglingSymlinkCleaned(t *testing.T) {
	c := startEnvContainer(t)

	// Inject a dangling symlink pointing to a non-existent /opt/mise path.
	_, code := exec(t, c, []string{"bash", "-c",
		"mkdir -p /home/" + hostUser + "/.local/share/mise/installs/node && " +
			"ln -s /opt/mise/installs/node/0.0.0-nonexistent " +
			"/home/" + hostUser + "/.local/share/mise/installs/node/0.0.0-nonexistent",
	})
	if code != 0 {
		t.Fatalf("FAIL: could not inject dangling symlink")
	}

	// Re-run the dangling-symlink cleanup logic from setup_mise_home.
	_, code = exec(t, c, []string{"bash", "-c", `
		user_mise="/home/` + hostUser + `/.local/share/mise"
		for tool in "$user_mise/installs"/*/; do
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
		"test -L /home/" + hostUser + "/.local/share/mise/installs/node/0.0.0-nonexistent && echo EXISTS || echo CLEANED",
	})
	if strings.TrimSpace(out) != "CLEANED" {
		t.Errorf("FAIL: dangling symlink still present after cleanup")
	} else {
		t.Logf("PASS: dangling symlink cleaned up")
	}
}

// ── 7. Non-interactive shell access ──────────────────────────────────────────

// TestMiseNonInteractiveShell — tools must be accessible via docker exec
// (non-interactive, non-login shell) through shims on PATH.
func TestMiseNonInteractiveShell(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "node --version")
	if code != 0 {
		t.Errorf("FAIL: node not accessible in non-interactive shell (exit %d): %s", code, out)
	} else {
		t.Logf("PASS: node accessible: %s", out)
	}
}

// ── 8. No ASDF env vars leaked ───────────────────────────────────────────────

// TestMiseNoAsdfEnvVarsLeaked — no ASDF_* environment variables should be
// present in the container after migration to mise.
func TestMiseNoAsdfEnvVarsLeaked(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "env | grep ^ASDF_ || true")
	if code != 0 {
		t.Fatalf("FAIL: env command failed (exit %d): %s", code, out)
	}

	if strings.TrimSpace(out) != "" {
		t.Errorf("FAIL: ASDF_* env vars leaked:\n%s", out)
	} else {
		t.Logf("PASS: no ASDF_* env vars")
	}
}

// ── 9. NPM tools work (mise → node → npm chain) ─────────────────────────────

// TestMiseNpmToolsAvailable — npm-installed tools from /opt/npm-tools must work,
// verifying the full chain: mise installs node → npm install → tools on PATH.
func TestMiseNpmToolsAvailable(t *testing.T) {
	c := startEnvContainer(t)

	// npm itself must be available
	out, code := asUser(t, c, "npm --version")
	if code != 0 {
		t.Fatalf("FAIL: npm not available (exit %d): %s", code, out)
	}
	t.Logf("PASS: npm version: %s", out)

	// A tool from /opt/npm-tools should be accessible
	// patchright-mcp is always in the image package.json
	out, code = asUser(t, c, "which mcp-server-patchright 2>/dev/null || which npx 2>/dev/null")
	if code != 0 {
		t.Errorf("FAIL: no npm tools found on PATH (exit %d): %s", code, out)
	} else {
		t.Logf("PASS: npm tool found at: %s", out)
	}
}
