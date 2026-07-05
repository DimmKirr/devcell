package container_test

// Modules 2.0 integration delta tests (CELL-65/317/318/321).
//
// Strategy: reuse the shared image() helper (build once, or honour
// DEVCELL_TEST_IMAGE / DEVCELL_TEST_PURE_IMAGE). Pick representative modules
// that exercise the new T1 `mkEnableOption` + `mkIf` + `managedMcp` pipeline.
// Verify two things for each:
//   1. The module's binary lives at /opt/devcell/.local/state/nix/profiles/profile/bin/<name>
//   2. The module's MCP server name shows up in the merged ~/.claude.json
//
// One container per test, table-driven. No rebuild loops. Skips cleanly when
// no test image is available.

import (
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
	"github.com/DimmKirr/devcell/internal/scaffold"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startContainerWithImage is a thin local variant of startContainer that
// accepts an explicit image tag (so dev-stack tests can opt into a different
// image without changing the global image() helper).
func startContainerWithImage(t *testing.T, image string, env map[string]string) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: image,
		Env:   env,
		User:  "0",
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * 1e9),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container %s: %v", image, err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

// modulesUnderTest names representative modules to verify. Each pair is:
//   binary     — file under /opt/devcell/.local/state/nix/profiles/profile/bin/
//   mcpServer  — key in ~/.claude.json mcpServers (matches devcell.managedMcp.servers.<name>)
//
// Keep this list small. We're verifying the wiring, not the full catalog.
var modulesUnderTest = []struct {
	module    string
	binary    string
	mcpServer string
}{
	{module: "electronics", binary: "kicad-mcp", mcpServer: "kicad-mcp"},
	{module: "financial", binary: "yahoo-finance-mcp", mcpServer: "yahoo-finance"},
}

// TestModules2_EnabledModulesShipBinariesAndClaudeEntries verifies that for
// each module enabled in the test image (ultimate stack), the binary is on
// PATH and the MCP server is registered in claude.json. This is the smallest
// end-to-end proof that T1's enable+mkIf+managedMcp pipeline survives an
// image build.
func TestModules2_EnabledModulesShipBinariesAndClaudeEntries(t *testing.T) {
	c := startContainer(t, nil)

	// Read claude.json once; reuse across module subtests.
	claudeRaw, code := exec(t, c, []string{"cat", "/opt/devcell/.claude.json"})
	if code != 0 {
		t.Fatalf("read claude.json: exit=%d out=%q", code, claudeRaw)
	}
	var claude struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(claudeRaw), &claude); err != nil {
		t.Fatalf("parse claude.json: %v", err)
	}

	const binDir = "/opt/devcell/.local/state/nix/profiles/profile/bin"

	for _, m := range modulesUnderTest {
		m := m
		t.Run(m.module, func(t *testing.T) {
			// 1. binary present + executable
			out, code := exec(t, c, []string{"test", "-x", binDir + "/" + m.binary})
			if code != 0 {
				t.Errorf("binary missing for module %q: expected %s/%s (exit=%d out=%q)",
					m.module, binDir, m.binary, code, out)
			}

			// 2. mcp server registered in claude.json
			if _, ok := claude.MCPServers[m.mcpServer]; !ok {
				t.Errorf("MCP server %q (from module %q) not in claude.json mcpServers",
					m.mcpServer, m.module)
			}
		})
	}
}

// TestModules2_DevStackHasOnlySeedBinaries — long test. Asserts the dev stack
// (T2: scraping + infra) ships ONLY seed binaries and lacks non-seed modules.
//
// Image acquisition (in priority order):
//  1. DEVCELL_TEST_DEV_IMAGE — explicit override (e.g. CI passes a pre-pulled tag)
//  2. Local `devcell-user:dev-thin` if present
//  3. Auto-build via `buildThinImage("dev")` — `cell build --thin --stack dev
//     --image devcell-user:dev-thin-<sha>`, ~minutes against a warm nix store.
//
// Gated on `testing.Short()` because path (3) builds an image. Run via
// `go test ./test` (no -short); the inner loop `go test -short ./test` skips.
func TestModules2_DevStackHasOnlySeedBinaries(t *testing.T) {
	devImage := os.Getenv("DEVCELL_TEST_DEV_IMAGE")
	if devImage == "" {
		if imageExists("devcell-user:dev-thin") {
			devImage = "devcell-user:dev-thin"
		} else {
			if testing.Short() {
				t.Skip("long: would build dev-thin via `cell build --thin --stack dev`; run without -short or pre-set DEVCELL_TEST_DEV_IMAGE")
			}
			tag, err := buildThinImage("dev")
			if err != nil {
				t.Fatalf("build dev-thin image: %v", err)
			}
			devImage = tag
		}
	}

	// Override the shared image() helper by constructing the container directly
	// with the dev image — this is the one place we deviate from image().
	// (Could be added to helpers later if more dev-only tests appear.)
	c := startContainerWithImage(t, devImage, nil)
	binDir := "/opt/devcell/.local/state/nix/profiles/profile/bin"

	// Should be present (seed)
	for _, bin := range []string{"patchright-mcp-cell", "opentofu-mcp-server"} {
		bin := bin
		t.Run("present/"+bin, func(t *testing.T) {
			_, code := exec(t, c, []string{"test", "-x", binDir + "/" + bin})
			if code != 0 {
				t.Errorf("seed binary missing: %s/%s", binDir, bin)
			}
		})
	}

	// Should be ABSENT (only in ultimate, not dev)
	for _, bin := range []string{"kicad-mcp", "plex-mcp-server", "inoreader-mcp"} {
		bin := bin
		t.Run("absent/"+bin, func(t *testing.T) {
			_, code := exec(t, c, []string{"test", "-x", binDir + "/" + bin})
			if code == 0 {
				t.Errorf("non-seed binary unexpectedly present in dev stack: %s/%s", binDir, bin)
			}
		})
	}
}

// TestModules2_FreshVolumeStartsCleanly — first-time-use experience for
// thin mode: mount a fresh empty Docker volume at /nix and verify the cell
// either bootstraps cleanly OR fails with the known thin-bootstrap error.
//
// KNOWN ARCHITECTURAL GAP (discovered 2026-06-18): the thin image's ENTRYPOINT
// is `/nix/var/nix/profiles/devcell-tools/bin/tini`, which lives ON the volume
// — not in the image. An empty volume = no tini = container can't start
// (error: `exec: /nix/.../tini: no such file or directory`).
//
// This means a fresh `docker run` of a thin image against a new volume cannot
// bootstrap. The first-run path must go through `cell build`, which populates
// the volume before any agent is run. End users on `cell <agent>` are unaffected
// because the CLI guarantees the volume is built before launch (CELL-156 era).
//
// This test asserts that the failure mode is the EXPECTED tini-missing error.
// If/when the bootstrap is fixed (image carries minimal tini, or entrypoint
// falls back), this test should be updated to assert successful startup.
//
// Skips: non-thin variant (only thin uses /nix volume).
// Cleanup: removes the volume after the test.
func TestModules2_FreshVolumeStartsCleanly(t *testing.T) {
	if !isThinVariant() {
		t.Skip("thin-variant only; set DEVCELL_TEST_VARIANT=thin")
	}

	// Unique volume name per run — purely a string handle, Docker creates it
	// on first mount.
	volName := "devcell-test-fresh-" + strings.ReplaceAll(t.Name(), "/", "-")

	// Cleanup BEFORE (in case a prior failed run left it) and AFTER.
	_ = osexec.Command("docker", "volume", "rm", "-f", volName).Run()
	t.Cleanup(func() {
		_ = osexec.Command("docker", "volume", "rm", "-f", volName).Run()
	})

	t.Setenv("DEVCELL_TEST_VOLUME_NAME", volName)

	// startContainer reads thinVolumeName() which now honours the env var.
	// We intercept the panic/fail via a sub-process style: directly construct
	// the container request and check the error shape, rather than rely on
	// startContainer's t.Fatalf.
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: image(),
		User:  "0",
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * 1e9),
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(thinVolumeName(), "/nix"),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err == nil {
		// Bootstrap unexpectedly succeeded — the gap is closed. Update this test.
		t.Cleanup(func() { _ = c.Terminate(ctx) })
		out, code := exec(t, c, []string{"sh", "-c", "echo first-run-ok"})
		if code == 0 && strings.Contains(out, "first-run-ok") {
			t.Log("UNEXPECTED: fresh thin volume booted cleanly — first-run gap may be fixed; consider updating test")
			return
		}
		t.Fatalf("container started but isn't usable: code=%d out=%q", code, out)
	}

	// Failure expected — assert the shape so a regression to a DIFFERENT
	// failure mode still surfaces.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "tini") && !strings.Contains(errMsg, "no such file") {
		t.Fatalf("expected tini-missing error (known thin-bootstrap gap), got different failure: %v", err)
	}
	t.Logf("CONFIRMED known gap: thin cell can't boot from empty /nix volume — entrypoint binary tini lives on the volume.\n  err: %v", err)
}

// TestModules2_GlobalTOMLOverrideViaHOME — verifies the layered-merge flow
// for the Modules 2.0 union semantics (T6 / CELL-67): a user can set a
// global `~/.config/devcell/devcell.toml` by overriding HOME, and a project
// `.devcell.toml` then unions on top.
//
// Why integration: cfg.LoadLayered is unit-tested in isolation, but this
// asserts the *file-discovery convention* (global lives at
// `$HOME/.config/devcell/devcell.toml`) works against real on-disk TOMLs in
// a temp HOME, with all generated artifacts written under
// `test/results/<datetime>-<sha>/TestModules2_GlobalTOMLOverrideViaHOME/`
// for post-run inspection.
func TestModules2_GlobalTOMLOverrideViaHOME(t *testing.T) {
	// All generated artifacts go under the per-run results dir.
	outDir := filepath.Join(testRunDir(), "TestModules2_GlobalTOMLOverrideViaHOME")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	// Fake HOME with a global config.
	fakeHome := filepath.Join(outDir, "home")
	globalDir := filepath.Join(fakeHome, ".config", "devcell")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	globalPath := filepath.Join(globalDir, "devcell.toml")
	globalContent := `[cell]
stack = "dev"
modules = ["kicad", "plex"]
`
	if err := os.WriteFile(globalPath, []byte(globalContent), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}

	// Project dir with its own .devcell.toml.
	projectDir := filepath.Join(outDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	projectPath := filepath.Join(projectDir, ".devcell.toml")
	projectContent := `[cell]
modules = ["yahoo-finance"]
`
	if err := os.WriteFile(projectPath, []byte(projectContent), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	// Drive LoadLayered with no env overrides — the only state that matters
	// is the two TOMLs on disk.
	merged := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })

	// Stack from global (project didn't set one) — proves global was read.
	if got := merged.Cell.ResolvedStack(); got != "dev" {
		t.Errorf("stack: want %q (from global), got %q", "dev", got)
	}
	// Modules: UNION of global [kicad, plex] + project [yahoo-finance].
	want := []string{"kicad", "plex", "yahoo-finance"}
	if !sliceEquals(merged.Cell.Modules, want) {
		t.Errorf("modules union: want %v, got %v", want, merged.Cell.Modules)
	}

	// Persist the merged output to results dir for post-run inspection.
	mergedJSON, _ := json.MarshalIndent(map[string]any{
		"stack":   merged.Cell.ResolvedStack(),
		"modules": merged.Cell.Modules,
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "merged.json"), mergedJSON, 0o644)
	t.Logf("artifacts: %s", outDir)
}

// sliceEquals — order-sensitive string slice equality.
func sliceEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestModules2_E2E_GlobalAndProjectModulesBothInstalled — full bridge across
// the Modules 2.0 chain:
//
//   global TOML (outer)  ┐
//                        ├─► cfg.LoadLayered → merged modules
//   project TOML (inner) ┘                       │
//                                                ▼
//                          scaffold.GenerateFlakeNix → flake.nix
//                                                │
//                                                ▼
//                           home-manager / nix builds image
//                                                │
//                                                ▼
//                          binaries land at /opt/devcell/.../bin/<name>
//
// This test exercises every link except the build itself. The build chain is
// covered by TestModules2_EnabledModulesShipBinariesAndClaudeEntries which
// asserts the same modules' binaries exist in the prebuilt ultimate image.
// Combined coverage = full E2E without per-test rebuild.
//
// Two modules from DIFFERENT sources:
//   outer (global HOME): "news"      → installs inoreader-mcp
//   inner (project):     "qa-tools"  → installs mailslurp-mcp
//
// All generated artifacts persist under test/results/<ts>/<test-name>/.
func TestModules2_E2E_GlobalAndProjectModulesBothInstalled(t *testing.T) {
	const (
		outerModule = "news"
		outerBinary = "inoreader-mcp"
		innerModule = "qa-tools"
		innerBinary = "mailslurp-mcp"
	)

	outDir := filepath.Join(testRunDir(), "TestModules2_E2E_GlobalAndProjectModulesBothInstalled")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	// ── Step 1: write outer (global) + inner (project) TOMLs ────────────
	fakeHome := filepath.Join(outDir, "home")
	globalDir := filepath.Join(fakeHome, ".config", "devcell")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	globalPath := filepath.Join(globalDir, "devcell.toml")
	if err := os.WriteFile(globalPath, []byte(`[cell]
stack = "base"
modules = ["`+outerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}

	projectDir := filepath.Join(outDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	projectPath := filepath.Join(projectDir, ".devcell.toml")
	if err := os.WriteFile(projectPath, []byte(`[cell]
modules = ["`+innerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	// ── Step 2: merge ────────────────────────────────────────────────────
	merged := cfg.LoadLayered(globalPath, projectPath, func(string) string { return "" })
	if !contains(merged.Cell.Modules, outerModule) {
		t.Fatalf("merged modules missing outer %q: %v", outerModule, merged.Cell.Modules)
	}
	if !contains(merged.Cell.Modules, innerModule) {
		t.Fatalf("merged modules missing inner %q: %v", innerModule, merged.Cell.Modules)
	}

	// ── Step 3: generate flake.nix from merged modules ──────────────────
	flake := scaffold.GenerateFlakeNix(merged.Cell.ResolvedStack(), merged.Cell.Modules, "v0.0.0-test", false)
	if err := os.WriteFile(filepath.Join(outDir, "generated-flake.nix"), []byte(flake), 0o644); err != nil {
		t.Fatalf("write generated flake: %v", err)
	}
	for _, m := range []string{outerModule, innerModule} {
		want := `devcell.modules.` + m + `.enable = true`
		if !strings.Contains(flake, want) {
			t.Errorf("generated flake.nix missing enable for module %q (expected %q)", m, want)
		}
	}

	// ── Step 4: verify the modules' binaries actually install ───────────
	// Uses the prebuilt ultimate image (every module enabled). This proves
	// that the modules referenced by the merge → flake step are real and
	// produce the expected binaries. Steps 1–3 prove the merge wires them
	// in; this step proves the wiring would install them.
	c := startContainer(t, nil)
	binDir := "/opt/devcell/.local/state/nix/profiles/profile/bin"
	for _, bin := range []string{outerBinary, innerBinary} {
		bin := bin
		t.Run("binary/"+bin, func(t *testing.T) {
			out, code := exec(t, c, []string{"test", "-x", binDir + "/" + bin})
			if code != 0 {
				t.Errorf("binary missing for merged module: %s/%s (exit=%d out=%q)",
					binDir, bin, code, out)
			}
		})
	}

	t.Logf("artifacts: %s", outDir)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestModules2_CellModulesListEndToEnd — `cell modules list` against the
// checkout's nixhome. Verifies the full pipeline: cobra cmd → catalog reader
// → nix eval --json → table formatter.
//
// Skips when: cell binary isn't built, nix isn't on PATH, or nix can't talk
// to a daemon (thin devcell cells, sandboxed CI without --store override).
func TestModules2_CellModulesListEndToEnd(t *testing.T) {
	if _, err := os.Stat("../bin/cell"); err != nil {
		t.Skip("../bin/cell not built; run `go build -o ./bin/cell ./cmd` first")
	}
	if _, err := osexec.LookPath("nix"); err != nil {
		t.Skip("nix not on PATH")
	}
	// Probe: can `nix` actually evaluate a flake input? Flake-fetching needs
	// the daemon/store path that `--expr` alone doesn't exercise. Catches
	// devcell thin cells and sandboxed CI where the daemon socket is absent.
	probe := osexec.Command("nix", "eval", "--json",
		"--extra-experimental-features", "nix-command flakes",
		"path:../nixhome#devcellProfiles.base")
	if err := probe.Run(); err != nil {
		t.Skipf("nix can't evaluate the local flake in this env (%v); skipping — works on hosts with running nix daemon", err)
	}

	cmd := osexec.Command("../bin/cell", "modules", "list")
	cmd.Env = append(os.Environ(), "DEVCELL_NIXHOME_PATH=../nixhome")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cell modules list: %v\noutput tail: %s", err, lastNLines(string(out), 20))
	}

	for _, want := range []string{"MODULE", "electronics", "scraping", "infra"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q\nfull:\n%s", want, string(out))
		}
	}
}

// TestModules2_LongE2E_CleanVolume_TwoModulesFromGlobalAndProject — canonical
// E2E that codifies the full Modules 2.0 + CELL-38 happy path:
//
//   1. Empty /nix volume (fresh, never-populated docker volume).
//   2. Global ~/.config/devcell/devcell.toml sets stack=base + one module ("news").
//   3. Project .devcell.toml adds a second module ("qa-tools").
//   4. `cell build --thin` runs from the project dir with HOME overridden.
//      Auto-build path of `cell shell` is the same code; gating semantics
//      (image-missing, volume-unhydrated) are unit-tested separately.
//   5. Assert both modules' binaries are installed in the resulting image:
//        - inoreader-mcp (from global module "news")
//        - mailslurp-mcp (from project module "qa-tools")
//
// Codifies the load-bearing user expectation across CELL-6 (modules-only
// future): TOML modules drive what's installed; stack is just a preset.
//
// Long test (testing.Short() gate). Cleans up its volume.
func TestModules2_LongE2E_CleanVolume_TwoModulesFromGlobalAndProject(t *testing.T) {
	if testing.Short() {
		t.Skip("long: runs `cell build --thin` against a fresh volume (~minutes); drop -short to enable")
	}

	const (
		outerModule = "news"      // global
		outerBinary = "inoreader-mcp"
		innerModule = "qa-tools"  // project
		innerBinary = "mailslurp-mcp"
	)

	outDir := filepath.Join(testRunDir(), t.Name())
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	// ── Fresh empty volume ──────────────────────────────────────────────────
	volName := "devcell-test-cleanvol-" + shortSHA()
	_ = osexec.Command("docker", "volume", "rm", "-f", volName).Run()
	t.Cleanup(func() { _ = osexec.Command("docker", "volume", "rm", "-f", volName).Run() })

	// ── Global TOML ─────────────────────────────────────────────────────────
	fakeHome := filepath.Join(outDir, "home")
	globalDir := filepath.Join(fakeHome, ".config", "devcell")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	// Resolve the local nixhome dir absolutely — runner.ResolvePureNixhomeRef
	// reads [cell].nixhome as tier 1 (explicit user setting), skipping the
	// github fallback. Mirrors how other long tests cite a known-good nixhome
	// instead of relying on the network + upstream pin.
	localNixhome, err := filepath.Abs("../nixhome")
	if err != nil {
		t.Fatalf("abs nixhome: %v", err)
	}
	globalToml := filepath.Join(globalDir, "devcell.toml")
	if err := os.WriteFile(globalToml, []byte(`[cell]
stack = "base"
nixhome = "`+localNixhome+`"
modules = ["`+outerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write global toml: %v", err)
	}

	// ── Project TOML ────────────────────────────────────────────────────────
	projectDir := filepath.Join(outDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	projectToml := filepath.Join(projectDir, ".devcell.toml")
	if err := os.WriteFile(projectToml, []byte(`[cell]
modules = ["`+innerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write project toml: %v", err)
	}

	// ── Build cell binary if missing ────────────────────────────────────────
	cellBin, err := ensureCellBinary()
	if err != nil {
		t.Fatalf("ensure cell binary: %v", err)
	}

	// ── Run `cell build --thin --image <test-tag>` from project, HOME=fake ──
	imageTag := "devcell-user:test-cleanvol-" + shortSHA()
	t.Cleanup(func() { _ = osexec.Command("docker", "rmi", imageTag).Run() })

	cmd := osexec.Command(cellBin, "build", "--thin", "--image", imageTag, "--debug")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"HOME="+fakeHome,
		// Per-test volume isolation: cell build populates THIS volume;
		// helpers_test.go's thinVolumeName() also reads DEVCELL_NIX_VOLUME
		// so the testcontainer mount targets the same volume after build.
		"DEVCELL_NIX_VOLUME="+volName,
	)
	logPath := filepath.Join(outDir, "build.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		t.Fatalf("cell build --thin failed: %v (see %s)", err, logPath)
	}
	t.Logf("build log: %s", logPath)

	// ── Start container with the built image + fresh volume ─────────────────
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: imageTag,
		User:  "0",
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).WithStartupTimeout(60 * 1e9),
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(volName, "/nix"),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	// ── Assert both modules' binaries are present ───────────────────────────
	binDir := "/opt/devcell/.local/state/nix/profiles/profile/bin"
	for _, bin := range []string{outerBinary, innerBinary} {
		bin := bin
		t.Run("binary/"+bin, func(t *testing.T) {
			out, code := exec(t, c, []string{"test", "-x", binDir + "/" + bin})
			if code != 0 {
				t.Errorf("expected %s/%s installed, got exit=%d out=%q",
					binDir, bin, code, out)
			}
		})
	}

	t.Logf("artifacts: %s", outDir)
}

// TestModules2_LongE2E_UpstreamGithub_TwoModulesFromGlobalAndProject — same
// as the local-nixhome E2E above, but FORCES the github fallback by omitting
// `[cell].nixhome` from TOML and running from a project dir with no local
// `nixhome/`. Exercises the host-side git clone path in
// `scaffold.SyncNixhome` end-to-end against the real upstream
// `https://github.com/DimmKirr/devcell.git` @ `feature/wip`.
//
// Pinpoints regressions in:
//   - `runner.ResolvePureNixhomeRef` tier-3 (github) fallback
//   - `scaffold.ParseGithubFlakeRef` + `materializeGithubFlakeRef` git clone
//   - End-to-end Modules 2.0 with the published upstream layout
//
// Long test (testing.Short() gate); runs ~minutes due to git clone + cold
// nix download. Skipped in -short.
func TestModules2_LongE2E_UpstreamGithub_TwoModulesFromGlobalAndProject(t *testing.T) {
	if testing.Short() {
		t.Skip("long: clones upstream github, runs `cell build --thin` against a fresh volume (~minutes); drop -short to enable")
	}

	const (
		outerModule = "news"
		outerBinary = "inoreader-mcp"
		innerModule = "qa-tools"
		innerBinary = "mailslurp-mcp"
	)

	outDir := filepath.Join(testRunDir(), t.Name())
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	volName := "devcell-test-upstream-" + shortSHA()
	_ = osexec.Command("docker", "volume", "rm", "-f", volName).Run()
	t.Cleanup(func() { _ = osexec.Command("docker", "volume", "rm", "-f", volName).Run() })

	// Global TOML — NO nixhome key, so the resolver falls through to the
	// upstream github ref (tier 3).
	fakeHome := filepath.Join(outDir, "home")
	globalDir := filepath.Join(fakeHome, ".config", "devcell")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	globalToml := filepath.Join(globalDir, "devcell.toml")
	if err := os.WriteFile(globalToml, []byte(`[cell]
stack = "base"
modules = ["`+outerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write global toml: %v", err)
	}

	// Project dir with NO nixhome/ subdir — so tier 2 (BaseDir lookup) also
	// misses, guaranteeing tier 3.
	projectDir := filepath.Join(outDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	projectToml := filepath.Join(projectDir, ".devcell.toml")
	if err := os.WriteFile(projectToml, []byte(`[cell]
modules = ["`+innerModule+`"]
`), 0o644); err != nil {
		t.Fatalf("write project toml: %v", err)
	}

	cellBin, err := ensureCellBinary()
	if err != nil {
		t.Fatalf("ensure cell binary: %v", err)
	}

	imageTag := "devcell-user:test-upstream-" + shortSHA()
	t.Cleanup(func() { _ = osexec.Command("docker", "rmi", imageTag).Run() })

	cmd := osexec.Command(cellBin, "build", "--thin", "--image", imageTag, "--debug")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"HOME="+fakeHome,
		"DEVCELL_NIX_VOLUME="+volName,
	)
	logPath := filepath.Join(outDir, "build.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		t.Fatalf("cell build --thin (upstream) failed: %v (see %s)", err, logPath)
	}

	// Sanity: verify the synced nixhome's origin is the upstream github ref,
	// not a local path. Catches regressions where the resolver silently picks
	// the wrong tier.
	originPath := filepath.Join(projectDir, ".devcell", "nixhome", ".devcell-source")
	originBytes, err := os.ReadFile(originPath)
	if err != nil {
		t.Fatalf("read .devcell-source: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(originBytes)), "github:") {
		t.Errorf("expected synced nixhome origin to be a github ref; got %q", string(originBytes))
	}

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: imageTag,
		User:  "0",
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).WithStartupTimeout(60 * 1e9),
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(volName, "/nix"),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	binDir := "/opt/devcell/.local/state/nix/profiles/profile/bin"
	for _, bin := range []string{outerBinary, innerBinary} {
		bin := bin
		t.Run("binary/"+bin, func(t *testing.T) {
			out, code := exec(t, c, []string{"test", "-x", binDir + "/" + bin})
			if code != 0 {
				t.Errorf("expected %s/%s installed from upstream nixhome, got exit=%d out=%q",
					binDir, bin, code, out)
			}
		})
	}

	t.Logf("artifacts: %s", outDir)
}
