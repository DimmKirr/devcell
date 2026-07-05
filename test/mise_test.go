// mise_test.go — TDD tests for CELL-85: bake mise installs + shims into image,
// two-level shim PATH for reliable runtime tooling.
//
// L1: file-content / wiring validation (no Docker, no nix runtime needed)
//     - Verifies the Dockerfile and nixhome modules contain the design hooks.
//     - Failing L1 = the wiring is missing; impl needs to land first.
//
// L2: container exec — declared mise tools are on PATH and runnable.
//     - Uses the existing testcontainers harness (image() + startContainer + exec).
//     - Run via `task test:integration -- -run TestMise`.

package container_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// L1 — Wiring checks (file content)
// ---------------------------------------------------------------------------

// TestMise_DockerfileBakesShims asserts the Dockerfile runs `mise reshim` at
// build time with MISE_DATA_DIR pointed at the image-level baked shim dir.
// Without this, the impure path never produces /opt/devcell/.local/share/mise/shims/.
func TestMise_DockerfileBakesShims(t *testing.T) {
	dockerfile := readDockerfile(t)

	want := "/opt/devcell/.local/share/mise"
	if !strings.Contains(dockerfile, want+" mise reshim") &&
		!strings.Contains(dockerfile, "MISE_DATA_DIR="+want) {
		t.Fatalf("Dockerfile missing baked-shim step: expected `MISE_DATA_DIR=%s mise reshim` (or env-exported equivalent) somewhere in the build chain", want)
	}
}

// TestMise_DockerfileBakesShimsForReshim asserts the reshim invocation comes
// AFTER mise install (so there are installs to reshim against). Order matters.
func TestMise_DockerfileBakesShimsForReshim(t *testing.T) {
	dockerfile := readDockerfile(t)

	installIdx := strings.Index(dockerfile, "mise install")
	reshimIdx := strings.Index(dockerfile, "mise reshim")
	if installIdx == -1 {
		t.Fatal("Dockerfile has no `mise install` step — baseline broken")
	}
	if reshimIdx == -1 {
		t.Fatal("Dockerfile has no `mise reshim` step (TestMise_DockerfileBakesShims should also fail)")
	}
	if reshimIdx < installIdx {
		t.Fatalf("`mise reshim` (idx %d) comes BEFORE `mise install` (idx %d) — order must be install→reshim", reshimIdx, installIdx)
	}
}

// TestMise_SessionPathUserShimsOnly asserts nixhome/modules/mise.nix puts the
// user shims dir on home.sessionPath and does NOT re-add the retired baked
// shim dir — baked tools are resolved natively via MISE_SHARED_INSTALL_DIRS
// (CELL-75), not through a second shim level.
func TestMise_SessionPathUserShimsOnly(t *testing.T) {
	mise := readNixhomeFile(t, "modules/mise.nix")

	if !regexp.MustCompile(`(?s)home\.sessionPath\s*=\s*\[[^\]]*/\.local/share/mise/shims`).MatchString(mise) {
		t.Fatal("user shims dir missing from `home.sessionPath = [ ... ]` in mise.nix")
	}
	if regexp.MustCompile(`(?s)home\.sessionPath\s*=\s*\[[^\]]*"/opt/devcell/\.local/share/mise/shims"`).MatchString(mise) {
		t.Fatal("retired baked shim dir is back in home.sessionPath — shared installs (MISE_SHARED_INSTALL_DIRS) replaced the two-level shim design (CELL-75)")
	}
}

// TestMise_ShellRcUserShimsOnly asserts 05-shell-rc.sh's PATH export includes
// the user shims dir and no longer prepends the retired baked shim dir —
// baked tools resolve through user shims + MISE_SHARED_INSTALL_DIRS
// (CELL-75).
func TestMise_ShellRcUserShimsOnly(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/05-shell-rc.sh")

	pathLine := regexp.MustCompile(`(?m)^export PATH=.*$`).FindString(frag)
	if pathLine == "" {
		t.Fatal("no `export PATH=` line found in 05-shell-rc.sh — fragment shape changed")
	}
	if !strings.Contains(pathLine, "$HOME/.local/share/mise/shims") {
		t.Fatalf("user shims dir missing from 05-shell-rc.sh PATH export: %q", pathLine)
	}
	if strings.Contains(pathLine, "/opt/devcell/.local/share/mise/shims") {
		t.Fatalf("retired baked shim dir is back in 05-shell-rc.sh PATH export (two-level design was replaced by shared installs, CELL-75): %q", pathLine)
	}
}

// TestMise_EntrypointReshimNotSilenced asserts the runtime reshim in
// 10-mise.sh no longer silently swallows failures. The previous form
// `mise reshim 2>/dev/null || true` hid the very bug we're fixing.
func TestMise_EntrypointReshimNotSilenced(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	for _, line := range strings.Split(frag, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "mise") || !strings.Contains(trimmed, "reshim") {
			continue
		}
		if strings.Contains(trimmed, "2>/dev/null") && strings.Contains(trimmed, "|| true") {
			t.Fatalf("reshim line still silenced: %q — must log failures loudly", trimmed)
		}
	}
}

// TestMise_ReshimGatedByShaOnWarmBoot pins the steady-state-boot optimization.
// Pre-fix shape ran `mise reshim` on every cold boot, BEFORE the sha-gate
// that already proves whether `mise install -y` has work to do. On macOS
// bind mounts with a populated $HOME/.local/share/mise (~17k entries),
// reshim's walk costs ~30–40s per boot even when .tool-versions has not
// changed since the previous launch.
//
// Fix: the earliest `mise reshim` invocation in the fragment must sit
// INSIDE the global sha-gate (i.e. after `if [ -f "$HOME/.tool-versions" ]`
// opens), so it runs only on the install branch.
func TestMise_ReshimGatedByShaOnWarmBoot(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	gateOpen := strings.Index(frag, `if [ -f "$HOME/.tool-versions" ]`)
	if gateOpen == -1 {
		t.Fatal("global sha-gate marker `if [ -f \"$HOME/.tool-versions\" ]` not found — fragment shape changed")
	}

	// The fragment invokes reshim only as `"$mise_bin" reshim`. We match the
	// invocation form specifically — bare-prose "mise reshim" appears in
	// comments and would yield false positives if used as a fallback.
	reshimIdx := strings.Index(frag, `"$mise_bin" reshim`)
	if reshimIdx == -1 {
		t.Fatal("no `\"$mise_bin\" reshim` invocation found in 10-mise.sh — baseline broken or invocation form changed")
	}

	if reshimIdx < gateOpen {
		t.Fatalf("`mise reshim` (byte %d) runs BEFORE the sha-gate opens at byte %d.\n"+
			"Result: reshim executes on every boot regardless of whether .tool-versions changed,\n"+
			"walking ~17k files on the persistent bind mount (~30-40s on macOS hosts).\n"+
			"Move the reshim call inside the else branch of the sha-gate, alongside `mise install -y`.",
			reshimIdx, gateOpen)
	}
}

// TestMise_ChownsGatedByShaOnWarmBoot — same shape as the reshim test, but
// covers the two `chown -R` invocations (over $user_mise and over
// $HOME/.local/state/mise) that walk the same ~17k-file tree. They must
// also sit inside the global sha-gate so warm boots skip them.
func TestMise_ChownsGatedByShaOnWarmBoot(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	gateOpen := strings.Index(frag, `if [ -f "$HOME/.tool-versions" ]`)
	if gateOpen == -1 {
		t.Fatal("global sha-gate marker not found — fragment shape changed")
	}

	cases := []struct {
		name    string
		pattern string
	}{
		{"chown user_mise", `chown -R "$HOST_USER" "$user_mise"`},
		{"chown mise state", `chown -R "$HOST_USER" "$HOME/.local/state/mise"`},
		// Boot installs run as root and write lockfiles/metadata to
		// ~/.cache/mise even when versions resolve from the shared layer;
		// without this chown, user-level `mise install` (e.g. a project
		// .tool-versions pin) fails with EACCES on the lockfile dir.
		{"chown mise cache", `chown -R "$HOST_USER" "$HOME/.cache/mise"`},
	}
	for _, tc := range cases {
		idx := strings.Index(frag, tc.pattern)
		if idx == -1 {
			t.Errorf("%s: pattern %q not found in 10-mise.sh — baseline broken or fragment shape changed",
				tc.name, tc.pattern)
			continue
		}
		if idx < gateOpen {
			t.Errorf("%s: `%s` (byte %d) runs BEFORE the sha-gate opens at byte %d.\n"+
				"Result: a recursive chown walks the entire mise data dir on every warm boot.\n"+
				"Move this chown inside the else branch of the sha-gate.",
				tc.name, tc.pattern, idx, gateOpen)
		}
	}
}

// TestMise_EntrypointAlwaysCleansDanglingSymlinks asserts the entrypoint
// cleans dangling install symlinks unconditionally — NOT only when /opt/mise
// exists. Pure images don't have /opt/mise; if cleanup is gated on the cross-
// bind loop's outer iteration, leftover dangling symlinks from a prior image
// generation (impure → pure transition) persist and break `mise reshim`.
// Symptom: terraform / opentofu missing from PATH on every fresh pure cell.
func TestMise_EntrypointAlwaysCleansDanglingSymlinks(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	// The shared-installs design (CELL-75) keeps $HOME free of cross-tier
	// symlinks: the cleanup loop must remove install symlinks with ABSOLUTE
	// targets (legacy cross-bind into /opt/...) and dangling ones — but MUST
	// preserve mise's own relative version aliases (`24 -> ./24.16.0`,
	// `latest -> ...`). Deleting those forces a delete→recreate cycle and
	// invalidates the sha-gate on every boot (observed 2026-06-11: "Removing
	// legacy mise install symlink" spam + permanent cold boots).
	if !regexp.MustCompile(`\[ -L "\$link" \](?s).{0,400}rm -f "\$link"`).MatchString(frag) {
		t.Fatal("install-symlink cleanup loop not found in 10-mise.sh — old cross-bind symlinks would persist in $HOME and dangle on image rebuilds")
	}
	if !regexp.MustCompile(`readlink\s+"?\$link`).MatchString(frag) {
		t.Fatal("cleanup loop never inspects the symlink target (readlink) — it would also delete mise's own relative version aliases (24 -> ./24.16.0), forcing delete/recreate and a sha-gate invalidation on every boot")
	}
}

// TestMise_EntrypointNoCrossBind asserts the legacy cross-bind loop (symlink
// every baked tool version into $HOME) is gone. Baked installs are resolved
// read-only via MISE_SHARED_INSTALL_DIRS; symlinks into the bind-mounted
// home dangle whenever the image is rebuilt with different versions and
// break sibling cells of other image generations (CELL-75).
func TestMise_EntrypointNoCrossBind(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	for _, marker := range []string{`"$baked/installs"`, `/opt/mise/installs`} {
		if strings.Contains(frag, marker) {
			t.Fatalf("legacy cross-bind marker %q still present in 10-mise.sh — shared installs replaced the symlink design (CELL-75)", marker)
		}
	}
	if regexp.MustCompile(`ln -s\w*T?\s+"?\$ver_dir`).MatchString(frag) {
		t.Fatal("cross-bind `ln -s` of baked versions still present in 10-mise.sh")
	}
}

// TestMise_EntrypointInvalidatesShaOnStaleState pins the CELL-66 fix.
// When the cleanup loop wipes dangling install symlinks (or detects an empty
// tool dir), the sha-gate further down would otherwise still match
// ~/.tool-versions's checksum and skip `mise install -y` — leaving declared
// tools missing from PATH. The fix: invalidate .tv-global.sha (and the
// workspace variant) immediately after cleanup so the install pass runs.
//
// Assert structurally:
//  1. The fragment removes `.tv-global.sha` (an `rm -f ... .tv-global.sha`).
//  2. The removal happens BEFORE the sha-gate read (`sha256sum ".tool-versions"`).
//  3. The removal is conditional on a cleanup/stale-state flag (not a blanket
//     unconditional wipe — that would defeat the optimization on steady-state).
func TestMise_EntrypointInvalidatesShaOnStaleState(t *testing.T) {
	frag := readNixhomeFile(t, "modules/fragments/10-mise.sh")

	// 1) An `rm -f` line touching .tv-global.sha must exist.
	rmPattern := regexp.MustCompile(`rm\s+-f[^\n]*\.tv-global\.sha`)
	rmMatches := rmPattern.FindAllStringIndex(frag, -1)
	if len(rmMatches) == 0 {
		t.Fatal("entrypoint never removes .tv-global.sha — sha-gate will skip `mise install -y` even after cleanup wipes installs (CELL-66)")
	}

	// 2) The invalidation must come BEFORE the sha-gate read so the next
	//    install pass sees a missing/different sha and proceeds.
	rmIdx := rmMatches[0][0]
	gateIdx := strings.Index(frag, `sha256sum "$HOME/.tool-versions"`)
	if gateIdx == -1 {
		t.Fatal("sha-gate read (`sha256sum \"$HOME/.tool-versions\"`) not found — fragment shape changed")
	}
	if rmIdx > gateIdx {
		t.Fatalf("`.tv-global.sha` invalidation (idx %d) comes AFTER the sha-gate read (idx %d) — must precede so the gate sees the wiped sha", rmIdx, gateIdx)
	}

	// 3) Invalidation must be inside a conditional (not unconditional). We
	//    accept any `if` guard immediately above the rm line in the same
	//    block. Concretely: there must be an `if [ ... ]; then` between the
	//    cleanup loop and the rm. Looser check: the rm line is indented
	//    further than `setup_mise_home() {` opens its body, AND there's an
	//    `if ` token between the cleanup pattern and the rm.
	cleanupAnchor := strings.Index(frag, `[ -L "$link" ]`)
	if cleanupAnchor == -1 {
		t.Fatal("cleanup pattern not found — fragment shape changed (TestMise_EntrypointAlwaysCleansDanglingSymlinks should also fail)")
	}
	between := frag[cleanupAnchor:rmIdx]
	if !strings.Contains(between, "if ") {
		t.Fatal("`.tv-global.sha` invalidation is not guarded by an `if ...` — would defeat the sha-gate optimization on steady-state restarts")
	}
}

// TestMise_ImageNixStagesToolVersions asserts the pure-image build (image.nix)
// manually stages /etc/devcell/tool-versions because home.activation scripts
// (writeToolVersions in mise.nix) don't run at pure-image build time — only
// home.file content gets copied via homeConfig.activationPackage/home-files/.
//
// Without this stage, fresh pure cells have no ~/.tool-versions to install
// from at boot — `mise install -y` is gated on the file's existence in
// 10-mise.sh, so declared tools never get installed and reshim has nothing
// to generate shims for. User-visible: `command not found` for terraform,
// tofu, etc. on every pure cell launch.
func TestMise_ImageNixStagesToolVersions(t *testing.T) {
	imgNix := readNixhomeFile(t, "packages/image.nix")

	// Accept either an explicit `tool-versions` write step or a generic
	// "stage devcell.mise.tools content into /etc/devcell/tool-versions" pattern.
	// We check structural intent: the file path must appear AND the homeConfig's
	// mise.tools attribute must be referenced (so we know the content is sourced
	// from the live declarations, not a hardcoded list).
	if !strings.Contains(imgNix, "/etc/devcell/tool-versions") {
		t.Fatal("image.nix doesn't stage /etc/devcell/tool-versions — pure images won't have declared mise tools available on first boot")
	}
	if !strings.Contains(imgNix, "devcell.mise.tools") &&
		!strings.Contains(imgNix, "config.devcell.mise") {
		t.Fatal("image.nix references /etc/devcell/tool-versions but doesn't source content from `homeConfig.config.devcell.mise.tools` — content must be live, not hardcoded")
	}
}

// TestMise_AllDeclaredToolsCoveredByBakeStep — sanity check that the baseline
// `mise install` step exists; downstream reshim picks up whatever it produces.
func TestMise_AllDeclaredToolsCoveredByBakeStep(t *testing.T) {
	declared := declaredMiseTools(t)
	if len(declared) == 0 {
		t.Fatal("no devcell.mise.tools.* declarations found across nixhome modules — baseline broken")
	}

	dockerfile := readDockerfile(t)
	if !strings.Contains(dockerfile, "mise install") {
		t.Fatal("Dockerfile missing `mise install` step — declared tools won't be installed at build time")
	}

	t.Logf("declared mise tools (%d): %v", len(declared), declared)
}

// ---------------------------------------------------------------------------
// L2 — Container behavior (requires docker; skip otherwise)
// ---------------------------------------------------------------------------

// TestMise_DeclaredToolsOnPATH asserts every declared mise tool resolves on
// PATH in the session user's login shell inside a fresh container. This is
// the user-visible bug being fixed — the session user is the product
// surface (root login shells go through the base image's /etc/profile and
// are not provisioned by the entrypoint).
func TestMise_DeclaredToolsOnPATH(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, map[string]string{
		"APP_NAME":  "test",
		"HOST_USER": hostUser,
	})
	declared := declaredMiseTools(t)

	for _, tool := range declared {
		bin := miseBinaryName(tool)
		t.Run(tool, func(t *testing.T) {
			out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "command -v " + bin})
			if code != 0 || strings.TrimSpace(out) == "" {
				t.Fatalf("declared mise tool %q (binary %q) not on PATH: out=%q exit=%d", tool, bin, out, code)
			}
			t.Logf("%s → %s", tool, strings.TrimSpace(out))
		})
	}
}

// TestMise_SharedInstalls_DeclaredToolsShared asserts mise resolves every
// declared tool from the read-only baked install dir via
// MISE_SHARED_INSTALL_DIRS (mise ≥2026.3.9 native shared installs, CELL-75)
// — `mise ls` must tag them "(shared)". This is the provenance check: a tool
// that was silently re-downloaded into the user data dir would resolve too,
// but without the tag.
func TestMise_SharedInstalls_DeclaredToolsShared(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, map[string]string{
		"APP_NAME":  "test",
		"HOST_USER": hostUser,
	})

	out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "mise ls 2>/dev/null"})
	if code != 0 {
		t.Fatalf("mise ls failed: exit=%d out=%q", code, out)
	}

	declared := declaredMiseTools(t)
	for _, tool := range declared {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), tool) && strings.Contains(line, "(shared)") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("declared tool %q not resolved from shared install dir (no \"(shared)\" tag in mise ls):\n%s", tool, out)
		}
	}
}

// TestMise_TerraformAndOpentofuOnPATH pins the specific bug (terraform and
// opentofu shims silently missing pre-fix). Named explicitly so future
// readers can grep for the regression.
func TestMise_TerraformAndOpentofuOnPATH(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, map[string]string{
		"APP_NAME":  "test",
		"HOST_USER": hostUser,
	})

	for _, bin := range []string{"terraform", "tofu"} {
		t.Run(bin, func(t *testing.T) {
			out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "command -v " + bin})
			if code != 0 || strings.TrimSpace(out) == "" {
				t.Fatalf("%s missing from PATH (this was the pre-CELL-85 bug): out=%q exit=%d", bin, out, code)
			}
		})
	}
}

// TestMise_Layering_LocalToolVersionsOverridesShared pins the full layering
// contract ("PATH, but for mise installs" — CELL-75):
//  1. no local pin → the tool resolves from the read-only shared (baked) layer
//  2. a project .tool-versions pinning a different version wins over shared
//  3. the missing version auto-installs (exec_auto_install defaults to true)
//     into the USER layer — never into the read-only shared dir
func TestMise_Layering_LocalToolVersionsOverridesShared(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, map[string]string{
		"APP_NAME":  "test",
		"HOST_USER": hostUser,
	})

	versionRe := regexp.MustCompile(`(?m)^v\d+\.\d+\.\d+$`)

	// 1. Baseline: resolves the shared baked version (no local pin).
	out, code := exec(t, c, []string{"gosu", hostUser, "bash", "-lc", "cd && node --version"})
	if code != 0 {
		t.Fatalf("baseline node --version failed: exit=%d out=%q", code, out)
	}
	baseline := versionRe.FindString(out)
	if baseline == "" {
		t.Fatalf("no version in baseline output: %q", out)
	}
	// The pin below must differ from the baked version or the test proves nothing.
	const pin = "26.1.0"
	if baseline == "v"+pin {
		t.Fatalf("baked node is now %s — update the pinned test version to keep layers distinct", baseline)
	}

	// 2+3. Project dir pins a version absent from both layers → shim must
	// auto-install it into the user layer and run it.
	out, code = exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		`mkdir -p ~/app && echo "node ` + pin + `" > ~/app/.tool-versions && cd ~/app && node --version 2>/dev/null`})
	if code != 0 {
		t.Fatalf("pinned node --version failed (shim auto-install broken?): exit=%d out=%q", code, out)
	}
	if got := versionRe.FindString(out); got != "v"+pin {
		t.Fatalf("project .tool-versions must override shared layer: got %q want %q (baseline %s)", got, "v"+pin, baseline)
	}

	// Auto-install landed in the user layer; the shared dir stayed read-only.
	out, _ = exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		`test -d "$HOME/.local/share/mise/installs/node/` + pin + `" && echo USER-LAYER-OK; ` +
			`test ! -e "/opt/devcell/.local/share/mise/installs/node/` + pin + `" && echo SHARED-UNTOUCHED`})
	for _, want := range []string{"USER-LAYER-OK", "SHARED-UNTOUCHED"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s marker — wrong layer received the auto-install:\n%s", want, out)
		}
	}
}

// TestMise_SharedInstalls_NoUserCopies asserts the user data dir contains no
// copies or symlinks of the baked tools after boot — the old cross-bind
// design symlinked every baked version into $HOME (dangling-link hazard on
// image rebuilds, CELL-75); the shared-installs design must not.
func TestMise_SharedInstalls_NoUserCopies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping L2 in -short mode")
	}
	c := startContainer(t, map[string]string{
		"APP_NAME":  "test",
		"HOST_USER": hostUser,
	})

	// Only cross-tier links (absolute targets into /opt or elsewhere) are
	// forbidden — mise's own relative version aliases (24 -> ./24.16.0) are
	// legitimate and must survive boot.
	out, _ := exec(t, c, []string{"gosu", hostUser, "bash", "-lc",
		`find "$HOME/.local/share/mise/installs" -maxdepth 2 -type l -lname '/*' 2>/dev/null; true`})
	if links := strings.TrimSpace(out); links != "" {
		t.Errorf("user mise installs dir contains absolute-target symlinks (legacy cross-bind still active):\n%s", links)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func readNixhomeFile(t *testing.T, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "nixhome", relPath))
	if err != nil {
		t.Fatalf("read nixhome/%s: %v", relPath, err)
	}
	return string(data)
}

// readImagesDockerfile returns the contents of images/Dockerfile (impure build path).
func readImagesDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "images", "Dockerfile"))
	if err != nil {
		t.Fatalf("read images/Dockerfile: %v", err)
	}
	return string(data)
}

// declaredMiseTools parses nixhome/modules/*.nix for `devcell.mise.tools.<name> = "..."`
// declarations and returns the list of tool names.
func declaredMiseTools(t *testing.T) []string {
	t.Helper()
	modulesDir := filepath.Join("..", "nixhome", "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		t.Fatalf("read nixhome/modules: %v", err)
	}
	// Match `devcell.mise.tools.<name> = "..."` at line start (optional whitespace)
	// — commented-out lines (e.g. `# devcell.mise.tools.python = "3.13.2";` in
	// python.nix) must NOT match, otherwise tests assert against tools that are
	// not in home.packages mise.tools and have no shim.
	re := regexp.MustCompile(`(?m)^\s*devcell\.mise\.tools\.([a-z0-9_-]+)\s*=\s*"`)
	seen := map[string]bool{}
	var tools []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".nix") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(modulesDir, e.Name()))
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			name := m[1]
			if !seen[name] {
				seen[name] = true
				tools = append(tools, name)
			}
		}
	}
	return tools
}

// TestMise_DeclaredMiseToolsParser_SkipsCommented exercises the parser regex
// in declaredMiseTools against synthetic input: line-anchored matches must
// skip commented declarations and accept whitespace-indented real ones.
// Locks in the fix for the original bug where the parser matched
// `# devcell.mise.tools.python = ...` and tests then demanded a python
// shim that didn't exist in the image's bake step.
func TestMise_DeclaredMiseToolsParser_SkipsCommented(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{"plain declaration", `devcell.mise.tools.go = "1.26.0";`, true},
		{"indented declaration", `  devcell.mise.tools.terraform = "1.14.3";`, true},
		{"tab-indented declaration", "\tdevcell.mise.tools.node = \"24.13.1\";", true},
		{"hash-commented", `# devcell.mise.tools.python = "3.13.2";`, false},
		{"hash-commented indented", `  # devcell.mise.tools.python = "3.13.2";`, false},
		{"hash with no space", `#devcell.mise.tools.python = "3.13.2";`, false},
		{"mid-line text not at start", `foo = devcell.mise.tools.bar = "1";`, false},
	}
	re := regexp.MustCompile(`(?m)^\s*devcell\.mise\.tools\.([a-z0-9_-]+)\s*=\s*"`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := re.MatchString(tc.input)
			if got != tc.wantMatch {
				t.Fatalf("regex match on %q: got %v, want %v", tc.input, got, tc.wantMatch)
			}
		})
	}
}

// miseBinaryName maps a mise tool key to the binary name when they differ.
func miseBinaryName(miseTool string) string {
	switch miseTool {
	case "opentofu":
		return "tofu"
	default:
		return miseTool
	}
}
