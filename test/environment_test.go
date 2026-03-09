package container_test

// environment_test.go — TDD tests for the /opt/devcell refactor.
//
// RED  (current image): TestNixAvailable fails — nix not in session user PATH.
// GREEN (after refactor): all tests pass.
//
// Run against current image to capture baseline:
//   go test -v -run TestEnv ./...
// Run against refactored image:
//   DEVCELL_TEST_IMAGE=ghcr.io/dimmkirr/devcell:opt-devcell go test -v -run TestEnv ./...

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const hostUser = "testuser"

// ── helpers ───────────────────────────────────────────────────────────────────

func startEnvContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	return startContainer(t, map[string]string{
		"HOST_USER": hostUser,
		"APP_NAME":  "test",
	})
}

// asUser runs cmd as the session user inside a login shell so ~/.bashrc is sourced.
func asUser(t *testing.T, c testcontainers.Container, cmd string) (string, int) {
	t.Helper()
	return exec(t, c, []string{"gosu", hostUser, "bash", "-lc", cmd})
}

// ── 1. Nix toolchain ─────────────────────────────────────────────────────────

// TestEnvNixAvailable — nix CLI must be on PATH for the session user.
//
// GREEN: /opt/devcell/.nix-profile/bin is on PATH unconditionally via .bashrc sed substitution.
func TestEnvNixAvailable(t *testing.T) {
	c := startEnvContainer(t)
	out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "nix --version"})
	if code != 0 {
		t.Fatalf("FAIL: nix not available for session user %q (exit %d)\n%s", hostUser, code, out)
	}
	t.Logf("PASS: %s", out)
}

// TestEnvNixProfilePath — /opt/devcell must exist and contain a .nix-profile.
//
// GREEN: homeDirectory = /opt/devcell hardcoded in flake.nix.
func TestEnvNixProfilePath(t *testing.T) {
	c := startEnvContainer(t)

	for _, path := range []string{
		"/opt/devcell",
		"/opt/devcell/.nix-profile",
		"/opt/devcell/.nix-profile/bin",
	} {
		_, code := exec(t, c, []string{"test", "-e", path})
		if code != 0 {
			t.Errorf("FAIL: %s does not exist", path)
		} else {
			t.Logf("PASS: %s exists", path)
		}
	}
}

// TestEnvCompatSymlink — /nix/var/nix/profiles/per-user/devcell/profile must resolve.
//
// GREEN: compat symlink hardcoded to devcell in Dockerfile.
func TestEnvCompatSymlink(t *testing.T) {
	c := startEnvContainer(t)
	out, code := exec(t, c, []string{"readlink", "-f", "/nix/var/nix/profiles/per-user/devcell/profile"})
	if code != 0 || !strings.Contains(out, "/nix/store/") {
		t.Fatalf("FAIL: /nix/var/nix/profiles/per-user/devcell/profile doesn't resolve to /nix/store (exit %d): %q", code, out)
	}
	t.Logf("PASS: compat symlink → %s", out)
}

// ── 2. Session identity ───────────────────────────────────────────────────────

// TestEnvSessionIdentity — $HOME and $USER must match HOST_USER (session user, not nix owner devcell).
func TestEnvSessionIdentity(t *testing.T) {
	c := startEnvContainer(t)

	expectedHome := "/home/" + hostUser

	cases := []struct {
		name     string
		cmd      string
		contains string
	}{
		{"whoami", "whoami", hostUser},
		{"HOME", "echo $HOME", expectedHome},
		{"USER", "echo $USER", hostUser},
	}

	for _, tc := range cases {
		out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", tc.cmd})
		if code != 0 || !strings.Contains(out, tc.contains) {
			t.Errorf("FAIL %s: want %q, got %q (exit %d)", tc.name, tc.contains, out, code)
		} else {
			t.Logf("PASS %s: %q", tc.name, out)
		}
	}
}

// ── 3. Write paths ───────────────────────────────────────────────────────────

// TestEnvWritePaths — GOPATH and home must be writable by the session user.
// GOPATH must NOT point into /opt/devcell (read-only for session user).
//
// GREEN: entrypoint substitutes /opt/devcell → /home/$HOST_USER in .bashrc via sed.
func TestEnvWritePaths(t *testing.T) {
	c := startEnvContainer(t)

	// GOPATH must not be inside /opt/devcell
	gopath, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "go env GOPATH"})
	if code != 0 {
		t.Fatalf("FAIL: could not get GOPATH (exit %d): %s", code, gopath)
	}
	if strings.HasPrefix(gopath, "/opt/devcell") {
		t.Errorf("FAIL: GOPATH=%q points into /opt/devcell — session user can't write there", gopath)
	} else {
		t.Logf("PASS: GOPATH=%q (not in /opt/devcell)", gopath)
	}

	// GOPATH must be writable
	probe := gopath + "/.write-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		fmt.Sprintf("mkdir -p %q && rmdir %q", probe, probe)})
	if code != 0 {
		t.Errorf("FAIL: GOPATH=%q is not writable by session user", gopath)
	} else {
		t.Logf("PASS: GOPATH=%q is writable", gopath)
	}

	// $HOME must be writable
	_, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		"touch ~/.write-test && rm ~/.write-test"})
	if code != 0 {
		t.Errorf("FAIL: $HOME is not writable by session user")
	} else {
		t.Logf("PASS: $HOME is writable")
	}

	// /opt/devcell must NOT be writable by session user (it's the nix env, owned by devcell)
	_, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		"touch /opt/devcell/.write-test 2>/dev/null"})
	if code == 0 {
		t.Errorf("FAIL: session user can write to /opt/devcell — should be read-only")
		// cleanup
		exec(t, c, []string{"rm", "-f", "/opt/devcell/.write-test"}) //nolint
	} else {
		t.Logf("PASS: /opt/devcell is read-only for session user")
	}
}

// ── 4. Base image permissions ────────────────────────────────────────────────

// TestEnvBasePermissions — /opt/devcell directories must be owned by devcell (uid 1000).
// Regression: COPY/mkdir in Dockerfile can create intermediate dirs as root,
// breaking home-manager switch (flake.lock write) and nix config setup.
func TestEnvBasePermissions(t *testing.T) {
	c := startEnvContainer(t)

	dirs := []string{
		"/opt/devcell",
		"/opt/devcell/.config",
		"/opt/devcell/.config/nix",
		"/opt/devcell/.config/home-manager",
		"/opt/devcell/.nix-profile",
		"/opt/asdf",
		"/opt/npm-tools",
		"/opt/python-tools",
	}

	for _, dir := range dirs {
		out, code := exec(t, c, []string{"stat", "-c", "%u:%g", dir})
		if code != 0 {
			t.Errorf("FAIL: %s does not exist", dir)
			continue
		}
		if out != "1000:1000" {
			t.Errorf("FAIL: %s owned by %s, want 1000:1000", dir, out)
		} else {
			t.Logf("PASS: %s owned by %s", dir, out)
		}
	}
}

// ── 5. Startup time ──────────────────────────────────────────────────────────

// TestEnvStartupTime — container must reach ready state within budget.
//
// Before refactor: ~15-25s (home-manager switch + asdf plugin updates).
// After refactor target: <5s (no home-manager switch at startup).
func TestEnvStartupTime(t *testing.T) {
	const budgetSeconds = 10 // generous — tighten after refactor confirmed

	start := time.Now()
	_ = startContainer(t, map[string]string{
		"HOST_USER": hostUser,
		"APP_NAME":  "test",
	})
	elapsed := time.Since(start)

	t.Logf("Startup time: %.1fs (budget: %ds)", elapsed.Seconds(), budgetSeconds)
	if elapsed > time.Duration(budgetSeconds)*time.Second {
		t.Errorf("FAIL: startup took %.1fs, over %ds budget", elapsed.Seconds(), budgetSeconds)
	} else {
		t.Logf("PASS: within budget")
	}
}
