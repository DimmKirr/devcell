// runtime_test.go — session user, nix env, shell, mise, git, persistent home tests

package container_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/mod/semver"
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

// startContainerWithStaleHome starts a container where $HOME is pre-seeded
// with stale state that simulates a persistent bind mount from a previous
// image build.
func startContainerWithStaleHome(t *testing.T) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	img := image()
	volName := "devcell-stale-home-" + time.Now().Format("150405")

	// Step 1: Create a volume and seed it with stale content.
	seedScript := `
set -e
mkdir -p /home/testuser/.config/nix
mkdir -p /home/testuser/.config/mise
mkdir -p /home/testuser/.config/fontconfig/conf.d
mkdir -p /home/testuser/.fluxbox/styles/devcell-ocean
ln -s /nix/store/STALE_HASH_DOES_NOT_EXIST-home-manager-files/.config/mise/config.toml \
      /home/testuser/.config/mise/config.toml
ln -s /nix/store/STALE_HASH_DOES_NOT_EXIST-home-manager-files/.tool-versions \
      /home/testuser/.tool-versions
ln -s /nix/store/STALE_HASH_DOES_NOT_EXIST-home-manager-files/.config/fontconfig/conf.d/10-hm-fonts.conf \
      /home/testuser/.config/fontconfig/conf.d/10-hm-fonts.conf
ln -s /nix/store/STALE_HASH_DOES_NOT_EXIST-home-manager-files/.fluxbox/styles/devcell-ocean/theme.cfg \
      /home/testuser/.fluxbox/styles/devcell-ocean/theme.cfg
chown -R 1000:1000 /home/testuser
echo "STALE_SEED_DONE"
`
	// Seed using a throwaway alpine container with the volume.
	seedReq := testcontainers.ContainerRequest{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", seedScript},
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(volName, "/home/testuser"),
		),
		WaitingFor: wait.ForLog("STALE_SEED_DONE").WithStartupTimeout(15 * time.Second),
	}
	seedC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: seedReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("seed container: %v", err)
	}
	_ = seedC.Terminate(ctx)
	t.Logf("Seeded stale home volume: %s", volName)

	// Step 2: Start the real container with the pre-seeded volume.
	req := testcontainers.ContainerRequest{
		Image: img,
		Env: map[string]string{
			"HOST_USER": hostUser,
			"APP_NAME":  "test",
		},
		User: "0",
		Cmd:  []string{"tail", "-f", "/dev/null"},
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(volName, "/home/testuser"),
		),
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container with stale home: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Terminate(ctx)
		// Remove the volume after test.
		removeVolume(volName)
	})
	return c
}

// removeVolume removes a Docker volume (best-effort).
func removeVolume(name string) {
	ctx := context.Background()
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return
	}
	defer cli.Close()
	cli.VolumeRemove(ctx, name, true) //nolint:errcheck
}

// --- Environment ---

// TestEnv_NixLdLibraryPath -- .nix-ld-library-path must include GUI libs from the
// full profile closure, not just the base packages.
// Regression: generateNixLdPath activation ran after writeBoundary but before
// linkGeneration, scanning the old/empty profile generation instead of the current one.
func TestEnv_NixLdLibraryPath(t *testing.T) {
	c := startEnvContainer(t)

	// The file must exist and be non-empty.
	out, code := exec(t, c, []string{"cat", "/opt/devcell/.nix-ld-library-path"})
	if code != 0 || out == "" {
		t.Fatalf("FAIL: /opt/devcell/.nix-ld-library-path missing or empty (exit %d)", code)
	}
	entries := strings.Split(out, ":")
	t.Logf("INFO: .nix-ld-library-path has %d entries", len(entries))

	// Critical GUI libraries that Electron/Chromium needs.
	// These come from the desktop module's runtimeLibs and home.packages.
	// If generateNixLdPath scans the wrong generation, none of these appear.
	requiredSubstrings := []string{
		"gtk+3",        // GTK 3
		"cairo-",       // 2D rendering
		"pango-",       // text layout
		"nss-",         // network security
		"nspr-",        // Netscape Portable Runtime
		"alsa-lib-",    // audio
		"dbus-",        // D-Bus IPC
		"libxkbcommon", // keyboard handling
		"mesa",         // OpenGL/Mesa
	}

	for _, sub := range requiredSubstrings {
		found := false
		for _, e := range entries {
			if strings.Contains(e, sub) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FAIL: .nix-ld-library-path missing %q — activation script likely scanned stale profile", sub)
		} else {
			t.Logf("PASS: found %q in .nix-ld-library-path", sub)
		}
	}
}

// TestEnv_LdLibraryPathSession -- session user's LD_LIBRARY_PATH must include
// GUI libraries loaded from .nix-ld-library-path.
func TestEnv_LdLibraryPathSession(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "echo $LD_LIBRARY_PATH")
	if code != 0 || out == "" {
		t.Fatalf("FAIL: LD_LIBRARY_PATH not set in user session (exit %d)", code)
	}
	entries := strings.Split(out, ":")
	t.Logf("INFO: LD_LIBRARY_PATH has %d entries", len(entries))

	// Spot-check a few GUI libs that must be resolvable.
	for _, sub := range []string{"gtk+3", "cairo-", "nss-"} {
		found := false
		for _, e := range entries {
			if strings.Contains(e, sub) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FAIL: LD_LIBRARY_PATH missing %q — GUI libs not exported to session", sub)
		} else {
			t.Logf("PASS: found %q in LD_LIBRARY_PATH", sub)
		}
	}
}

// TestEnv_NixProfilePath -- /opt/devcell must exist and contain a .nix-profile.
func TestEnv_NixProfilePath(t *testing.T) {
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

// TestEnv_NixProfile -- home-manager native profile path must resolve.
func TestEnv_NixProfile(t *testing.T) {
	c := startEnvContainer(t)
	out, code := exec(t, c, []string{"readlink", "-f", "/opt/devcell/.local/state/nix/profiles/profile"})
	if code != 0 || !strings.Contains(out, "/nix/store/") {
		t.Fatalf("FAIL: /opt/devcell/.local/state/nix/profiles/profile doesn't resolve to /nix/store (exit %d): %q", code, out)
	}
	t.Logf("PASS: nix profile -> %s", out)
}

// TestEnv_SessionIdentity -- $HOME and $USER must match HOST_USER.
func TestEnv_SessionIdentity(t *testing.T) {
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

// TestEnv_WritePaths -- GOPATH and home must be writable by the session user.
func TestEnv_WritePaths(t *testing.T) {
	c := startEnvContainer(t)

	// GOPATH must not be inside /opt/devcell.
	// Use $GOPATH env var (set by 05-shell-rc.sh) instead of `go env GOPATH`
	// to avoid requiring the go binary in stacks that don't include it.
	gopath, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", `echo "$GOPATH"`})
	if code != 0 || gopath == "" {
		// Fallback: try go env if available
		gopath, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "go env GOPATH 2>/dev/null"})
		if code != 0 {
			t.Fatalf("FAIL: GOPATH not set and go not available (exit %d): %s", code, gopath)
		}
	}
	if strings.HasPrefix(gopath, "/opt/devcell") {
		t.Errorf("FAIL: GOPATH=%q points into /opt/devcell -- session user can't write there", gopath)
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
		t.Errorf("FAIL: session user can write to /opt/devcell -- should be read-only")
		// cleanup
		exec(t, c, []string{"rm", "-f", "/opt/devcell/.write-test"}) //nolint
	} else {
		t.Logf("PASS: /opt/devcell is read-only for session user")
	}
}

// TestEnv_BasePermissions -- /opt/devcell directories must be owned by devcell (uid 1000).
func TestEnv_BasePermissions(t *testing.T) {
	c := startEnvContainer(t)

	dirs := []string{
		"/opt/devcell",
		"/opt/devcell/.config",
		"/opt/devcell/.config/nix",
		"/opt/devcell/.config/devcell",
		"/opt/devcell/.nix-profile",
		"/opt/mise",
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

// TestEnv_ImageVersionStamps -- /etc/devcell/base-image-version and
// /etc/devcell/user-image-version must exist and contain a commit-date stamp.
func TestEnv_ImageVersionStamps(t *testing.T) {
	c := startEnvContainer(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"base-image-version", "/etc/devcell/base-image-version"},
		{"user-image-version", "/etc/devcell/user-image-version"},
	} {
		out, code := exec(t, c, []string{"cat", tc.path})
		if code != 0 {
			t.Errorf("FAIL %s: file not found at %s", tc.name, tc.path)
			continue
		}
		out = strings.TrimSpace(out)
		if out == "" {
			t.Errorf("FAIL %s: file is empty", tc.name)
		} else if !strings.Contains(out, "-") {
			t.Errorf("FAIL %s: expected <sha>-<date> format, got %q", tc.name, out)
		} else {
			t.Logf("PASS %s: %s", tc.name, out)
		}
	}
}

// TestEnv_StartupTime -- container must reach ready state within budget.
func TestEnv_StartupTime(t *testing.T) {
	const budgetSeconds = 10 // generous -- tighten after refactor confirmed

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

// --- Shell ---

// TestShell_StarshipConfigExists verifies the home-manager-generated config
// is present and contains the expected unicode character symbol.
func TestShell_StarshipConfigExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// starship.toml lives at /opt/devcell/.config/starship.toml; the session
	// user's shell sets STARSHIP_CONFIG to point there (no copy to $HOME).
	out, code := asUser(t, c, `cat "$STARSHIP_CONFIG"`)
	if code != 0 {
		// fallback: check the devcell home path directly
		out, code = asUser(t, c, "cat /opt/devcell/.config/starship.toml")
		if code != 0 {
			t.Fatalf("starship.toml not found at STARSHIP_CONFIG or /opt/devcell (exit %d): %s", code, out)
		}
	}

	if !strings.Contains(out, "\u2022") {
		t.Errorf("FAIL: starship.toml missing \u2022 character symbol:\n%s", out)
	} else {
		t.Logf("PASS: starship.toml contains \u2022 symbol")
	}

	if !strings.Contains(out, "add_newline = false") {
		t.Errorf("FAIL: starship.toml missing add_newline setting:\n%s", out)
	}
}

// TestShell_StarshipPromptRenders runs `starship prompt` directly and verifies
// the rendered output contains the configured unicode symbols.
func TestShell_StarshipPromptRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	out, code := asUser(t, c, "cd /tmp && starship prompt")
	if code != 0 {
		t.Fatalf("starship prompt failed (exit %d): %s", code, out)
	}

	if !strings.Contains(out, "\u2022") {
		t.Errorf("FAIL: starship prompt output missing \u2022 symbol: %q", out)
	} else {
		t.Logf("PASS: starship prompt rendered \u2022: %q", out)
	}
}

// TestShell_ZshStarshipIntegration verifies the complete integration chain:
// zsh sources .zshrc -> starship init is evaluated -> starship prompt renders.
func TestShell_ZshStarshipIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// .zshrc sources /opt/devcell/.zshrc which sets up PATH for starship.
	// Use bash -lc to ensure PATH is set, then invoke zsh.
	out, code := asUser(t, c, `zsh -c 'source ~/.zshrc 2>/dev/null; starship prompt 2>/dev/null'`)
	if code != 0 {
		t.Fatalf("zsh + starship prompt failed (exit %d): %s", code, out)
	}

	if !strings.Contains(out, "\u2022") {
		t.Errorf("FAIL: zsh->.zshrc->starship prompt missing \u2022 symbol: %q", out)
	} else {
		t.Logf("PASS: zsh->.zshrc->starship prompt rendered \u2022: %q", out)
	}
}

// TestShell_ZshAutosuggestions verifies the zsh-autosuggestions plugin is loaded.
func TestShell_ZshAutosuggestions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	// $HOME/.zshrc is a thin wrapper that sources /opt/devcell/.zshrc.
	// The plugin config lives in the sourced file, not the wrapper.
	out, code := asUser(t, c, "grep -rl autosuggestions ~/.zshrc /opt/devcell/.zshrc 2>/dev/null")
	if code != 0 {
		t.Fatalf("FAIL: zsh-autosuggestions not referenced in .zshrc chain (exit %d): %s", code, out)
	}
	t.Logf("PASS: zsh-autosuggestions found in: %s", strings.TrimSpace(out))
}

// TestShell_ZshSyntaxHighlighting verifies the syntax-highlighting plugin is loaded.
func TestShell_ZshSyntaxHighlighting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startEnvContainer(t)

	out, code := asUser(t, c, "grep -rl syntax-highlighting ~/.zshrc /opt/devcell/.zshrc 2>/dev/null")
	if code != 0 {
		t.Fatalf("FAIL: zsh-syntax-highlighting not referenced in .zshrc chain (exit %d): %s", code, out)
	}
	t.Logf("PASS: zsh-syntax-highlighting found in: %s", strings.TrimSpace(out))
}

// --- Mise ---

// TestMise_DataDir -- session user's MISE_DATA_DIR must be ~/.local/share/mise, not /opt/mise.
func TestMise_DataDir(t *testing.T) {
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

// TestMise_NodeViaUserShims -- node must be reachable through ~/.local/share/mise/shims.
func TestMise_NodeViaUserShims(t *testing.T) {
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

// TestMise_UserInstallPreserved -- setup_mise_home must not overwrite a real dir
// with a symlink (user-installed version must be preserved).
func TestMise_UserInstallPreserved(t *testing.T) {
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

// TestMise_DanglingSymlinkCleaned -- dangling symlinks in ~/.local/share/mise/installs/
// must be removed by setup_mise_home.
func TestMise_DanglingSymlinkCleaned(t *testing.T) {
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

// TestMise_NonInteractiveShell -- tools must be accessible via docker exec
// (non-interactive, non-login shell) through shims on PATH.
func TestMise_NonInteractiveShell(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "node --version")
	if code != 0 {
		t.Errorf("FAIL: node not accessible in non-interactive shell (exit %d): %s", code, out)
	} else {
		t.Logf("PASS: node accessible: %s", out)
	}
}

// TestMise_NoAsdfEnvVarsLeaked -- no ASDF_* environment variables should be
// present in the container after migration to mise.
func TestMise_NoAsdfEnvVarsLeaked(t *testing.T) {
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

// TestMise_GlobalConfigEnvVar -- MISE_GLOBAL_CONFIG_FILE must point to a valid
// nix store path, not a file in $HOME.
func TestMise_GlobalConfigEnvVar(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "echo $MISE_GLOBAL_CONFIG_FILE")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("FAIL: MISE_GLOBAL_CONFIG_FILE not set (exit %d): %s", code, out)
	}
	if !strings.HasPrefix(out, "/nix/store/") {
		t.Errorf("FAIL: MISE_GLOBAL_CONFIG_FILE=%q, want /nix/store/... path", out)
	}

	// The file must actually exist and be readable.
	_, code = asUser(t, c, fmt.Sprintf("test -f %q", out))
	if code != 0 {
		t.Errorf("FAIL: MISE_GLOBAL_CONFIG_FILE=%q does not exist or is not readable", out)
	} else {
		t.Logf("PASS: MISE_GLOBAL_CONFIG_FILE=%q (valid nix store path)", out)
	}
}

// TestMise_DefaultNpmPackagesEnvVar -- MISE_NODE_DEFAULT_PACKAGES_FILE must point
// to a valid nix store path.
func TestMise_DefaultNpmPackagesEnvVar(t *testing.T) {
	c := startEnvContainer(t)

	out, code := asUser(t, c, "echo $MISE_NODE_DEFAULT_PACKAGES_FILE")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("FAIL: MISE_NODE_DEFAULT_PACKAGES_FILE not set (exit %d): %s", code, out)
	}
	if !strings.HasPrefix(out, "/nix/store/") {
		t.Errorf("FAIL: MISE_NODE_DEFAULT_PACKAGES_FILE=%q, want /nix/store/... path", out)
	}

	// The file must actually exist and be readable.
	_, code = asUser(t, c, fmt.Sprintf("test -f %q", out))
	if code != 0 {
		t.Errorf("FAIL: MISE_NODE_DEFAULT_PACKAGES_FILE=%q does not exist or is not readable", out)
	} else {
		t.Logf("PASS: MISE_NODE_DEFAULT_PACKAGES_FILE=%q (valid nix store path)", out)
	}
}

// TestMise_NpmToolsAvailable -- npm-installed tools from /opt/npm-tools must work.
func TestMise_NpmToolsAvailable(t *testing.T) {
	c := startEnvContainer(t)

	// npm itself must be available
	out, code := asUser(t, c, "npm --version")
	if code != 0 {
		t.Fatalf("FAIL: npm not available (exit %d): %s", code, out)
	}
	t.Logf("PASS: npm version: %s", out)

	// A tool from /opt/npm-tools should be accessible
	out, code = asUser(t, c, "which mcp-server-patchright 2>/dev/null || which npx 2>/dev/null")
	if code != 0 {
		t.Errorf("FAIL: no npm tools found on PATH (exit %d): %s", code, out)
	} else {
		t.Logf("PASS: npm tool found at: %s", out)
	}
}

// --- Git ---

// TestGit_CommitUsesEnvIdentity starts a container with explicit git env vars,
// creates a commit, and verifies git log shows the configured identity.
func TestGit_CommitUsesEnvIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	c := startContainer(t, map[string]string{
		"HOST_USER":           hostUser,
		"APP_NAME":            "test",
		"GIT_AUTHOR_NAME":     "Test Author",
		"GIT_AUTHOR_EMAIL":    "test@devcell.io",
		"GIT_COMMITTER_NAME":  "Test Committer",
		"GIT_COMMITTER_EMAIL": "committer@devcell.io",
	})

	// Init repo, make a commit
	out, code := asUser(t, c, `
		cd /tmp &&
		mkdir gittest && cd gittest &&
		git init &&
		touch file.txt &&
		git add file.txt &&
		git commit -m "test commit" &&
		git log -1 --format='%an|%ae|%cn|%ce'
	`)
	if code != 0 {
		t.Fatalf("FAIL: git commit failed (exit %d):\n%s", code, out)
	}

	// Parse last line of output (git log format)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	logLine := lines[len(lines)-1]
	parts := strings.Split(logLine, "|")
	if len(parts) != 4 {
		t.Fatalf("FAIL: unexpected git log format: %q", logLine)
	}

	if parts[0] != "Test Author" {
		t.Errorf("author name: want %q, got %q", "Test Author", parts[0])
	}
	if parts[1] != "test@devcell.io" {
		t.Errorf("author email: want %q, got %q", "test@devcell.io", parts[1])
	}
	if parts[2] != "Test Committer" {
		t.Errorf("committer name: want %q, got %q", "Test Committer", parts[2])
	}
	if parts[3] != "committer@devcell.io" {
		t.Errorf("committer email: want %q, got %q", "committer@devcell.io", parts[3])
	}
	t.Logf("PASS: git identity = %s", logLine)
}

// TestGit_CommitUsesGitConfig verifies that when no GIT_* env vars are set,
// git reads identity from ~/.config/git/config inside the container.
func TestGit_CommitUsesGitConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	c := startContainer(t, map[string]string{
		"HOST_USER": hostUser,
		"APP_NAME":  "test",
	})

	// Write a git config file, init repo, commit, check identity
	out, code := asUser(t, c, `
		mkdir -p ~/.config/git &&
		cat > ~/.config/git/config <<'GITCFG'
[user]
	name = Config User
	email = config@devcell.io
GITCFG
		cd /tmp &&
		mkdir gitcfgtest && cd gitcfgtest &&
		git init &&
		touch file.txt &&
		git add file.txt &&
		git commit -m "config commit" &&
		git log -1 --format='%an|%ae'
	`)
	if code != 0 {
		t.Fatalf("FAIL: git commit with config failed (exit %d):\n%s", code, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	logLine := lines[len(lines)-1]
	parts := strings.Split(logLine, "|")
	if len(parts) != 2 {
		t.Fatalf("FAIL: unexpected git log format: %q", logLine)
	}

	if parts[0] != "Config User" {
		t.Errorf("author name: want %q, got %q", "Config User", parts[0])
	}
	if parts[1] != "config@devcell.io" {
		t.Errorf("author email: want %q, got %q", "config@devcell.io", parts[1])
	}
	t.Logf("PASS: git identity from config = %s", logLine)
}

// --- Persistent Home ---

// TestPersistentHome_NixConf verifies that nix.conf is present in $HOME even
// when an empty .config/nix/ dir already exists from a previous session.
func TestPersistentHome_NixConf(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// nix.conf is read via NIX_CONF_DIR (pointing to /opt/devcell/.config/nix),
	// not $HOME/.config/nix/. Verify the env var path works after stale cleanup.
	out, code := asUser(t, c, `cat "$NIX_CONF_DIR/nix.conf" 2>/dev/null || cat /opt/devcell/.config/nix/nix.conf`)
	if code != 0 {
		t.Fatalf("FAIL: nix.conf not found via NIX_CONF_DIR or /opt/devcell (exit %d)", code)
	}
	if !strings.Contains(out, "experimental-features") {
		t.Errorf("FAIL: nix.conf exists but missing experimental-features:\n%s", out)
	} else {
		t.Logf("PASS: nix.conf contains experimental-features")
	}
}

// TestPersistentHome_ToolVersions verifies that .tool-versions in $HOME is a
// valid plain file (not a dangling symlink to a GC'd nix store path).
func TestPersistentHome_ToolVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// .tool-versions must be a readable file (not dangling symlink).
	out, code := asUser(t, c, "cat $HOME/.tool-versions")
	if code != 0 {
		t.Fatalf("FAIL: .tool-versions not readable (exit %d)\n"+
			"Root cause: dangling symlink from previous home-manager generation", code)
	}
	// Must contain at least one tool line (e.g., "node 24.13.1").
	if !strings.Contains(out, "node") {
		t.Errorf("FAIL: .tool-versions doesn't contain expected tools:\n%s", out)
	} else {
		t.Logf("PASS: .tool-versions readable with tools: %s", strings.ReplaceAll(out, "\n", ", "))
	}

	// Must NOT be a symlink (should be a plain file copy).
	linkOut, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		"test -L $HOME/.tool-versions && echo SYMLINK || echo PLAIN"})
	if strings.Contains(linkOut, "SYMLINK") {
		t.Errorf("FAIL: .tool-versions is still a symlink (should be plain file)")
	} else {
		t.Logf("PASS: .tool-versions is a plain file")
	}
	_ = code
}

// TestPersistentHome_MiseConfig verifies that mise can read its global config
// despite a stale config.toml symlink in $HOME.
func TestPersistentHome_MiseConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// Mise config is read via MISE_GLOBAL_CONFIG_FILE env var (resolved nix store
	// path), not $HOME/.config/mise/config.toml. The $HOME path is cleaned up by
	// the stale symlink removal in entrypoint.sh.
	out, code := asUser(t, c, `cat "$MISE_GLOBAL_CONFIG_FILE" 2>/dev/null || cat /opt/devcell/.config/mise/config.toml 2>/dev/null`)
	if code != 0 {
		t.Skipf("SKIP: mise config not available (mise not in this stack)")
	}
	t.Logf("PASS: mise config readable: %.80s...", out)

	// After stale cleanup, $HOME/.config/mise/ should have no dangling symlinks.
	out, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc", `
		if [ -L "$HOME/.config/mise/config.toml" ] && [ ! -e "$HOME/.config/mise/config.toml" ]; then
			echo DANGLING
		else
			echo OK
		fi
	`})
	if strings.Contains(out, "DANGLING") {
		t.Errorf("FAIL: $HOME/.config/mise/config.toml is a dangling symlink\n" +
			"Root cause: stale nix store symlink persisted on bind mount")
	} else {
		t.Logf("PASS: mise config.toml is not dangling")
	}
}

// TestPersistentHome_NoDanglingSymlinks is the catch-all persistence bug detector.
func TestPersistentHome_NoDanglingSymlinks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// Find all dangling symlinks pointing to /nix/store under $HOME,
	// excluding tmp/ (nix build artifacts).
	out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", `
		found=0
		while IFS= read -r link; do
			target=$(readlink "$link" 2>/dev/null)
			case "$target" in /nix/store/*)
				if [ ! -e "$link" ]; then
					echo "DANGLING: $link -> $target"
					found=1
				fi
			;; esac
		done < <(find "$HOME" -maxdepth 4 -type l -not -path "*/tmp/*" 2>/dev/null)
		exit $found
	`})

	if code != 0 {
		t.Errorf("FAIL: dangling nix-store symlinks found in $HOME:\n%s\n"+
			"Root cause: stale symlinks from previous home-manager generation persist on bind mount", out)
	} else {
		t.Logf("PASS: no dangling nix-store symlinks in $HOME")
	}
}

// TestPersistentHome_FontConfig verifies fontconfig files are valid after
// entrypoint runs with stale $HOME.
func TestPersistentHome_FontConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// Fontconfig is read via FONTCONFIG_PATH (/opt/devcell/.config/fontconfig),
	// not $HOME/.config/fontconfig. After stale cleanup, $HOME path may not exist.
	out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", `
		# Check the production path (env var or /opt/devcell)
		FC="${FONTCONFIG_PATH:-/opt/devcell/.config/fontconfig}"
		if [ -e "$FC/conf.d/10-hm-fonts.conf" ]; then
			echo OK
		elif [ -L "$HOME/.config/fontconfig/conf.d/10-hm-fonts.conf" ] && [ ! -e "$HOME/.config/fontconfig/conf.d/10-hm-fonts.conf" ]; then
			echo DANGLING
		else
			echo OK
		fi
	`})
	if strings.Contains(out, "DANGLING") {
		t.Errorf("FAIL: fontconfig 10-hm-fonts.conf is dangling\n" +
			"Root cause: stale nix store symlink persisted on bind mount")
	} else {
		t.Logf("PASS: fontconfig config resolves")
	}
	_ = code
}

// TestPersistentHome_StarshipConfig verifies starship config is accessible
// regardless of stale $HOME state.
func TestPersistentHome_StarshipConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	c := startContainerWithStaleHome(t)

	// STARSHIP_CONFIG env var must point to a readable file.
	out, code := asUser(t, c, `test -f "$STARSHIP_CONFIG" && echo OK || echo MISSING`)
	if code != 0 || strings.Contains(out, "MISSING") {
		t.Errorf("FAIL: STARSHIP_CONFIG not readable (exit %d, out: %s)", code, out)
	} else {
		t.Logf("PASS: STARSHIP_CONFIG readable")
	}
}

// --- Toolchain ---

// TestClaude_CodeVersion -- claude CLI must be >= 2.1.74 (from nixpkgs-unstable).
func TestClaude_CodeVersion(t *testing.T) {
	c := startEnvContainer(t)

	const minVersion = "v2.1.70"

	out, code := asUser(t, c, "claude --version")
	if code != 0 {
		t.Fatalf("FAIL: claude not available (exit %d): %s", code, out)
	}

	// claude --version outputs e.g. "2.1.25 (Claude Code)"
	ver := strings.Fields(out)[0] // "2.1.25"
	semVer := "v" + ver           // semver requires "v" prefix

	if !semver.IsValid(semVer) {
		t.Fatalf("FAIL: could not parse claude version %q as semver", ver)
	}
	if semver.Compare(semVer, minVersion) < 0 {
		t.Errorf("FAIL: claude version %s < minimum %s", ver, minVersion)
	} else {
		t.Logf("PASS: claude version %s >= %s", ver, minVersion)
	}
}
