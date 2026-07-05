package container_test

// helpers — shared test infrastructure: image building, container lifecycle, exec.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	ultimateOnce sync.Once
	ultimateTag  string
	ultimateErr  error

	baseOnce sync.Once
	baseTag  string
	baseErr  error

	electronicsOnce sync.Once
	electronicsTag  string
	electronicsErr  error

	testdataOnce sync.Once
	testdataTag  string
	testdataErr  error

	// runDir is the per-run results directory: test/results/<datetime>-<sha>/
	runDir     string
	runDirOnce sync.Once
)

// minFreeDiskGB is the minimum free Docker VM disk needed for integration tests.
// Thin images are ~1.5GB + volume; pure/impure need ~30GB for image builds.
func minFreeDiskGB() int {
	if isThinVariant() {
		return 5
	}
	return 35
}

// checkDiskSpace probes the Docker VM filesystem via `docker run alpine df`
// and returns an error if available space is below minFreeDiskGB.
func checkDiskSpace() error {
	out, err := osexec.Command("docker", "run", "--rm", "alpine", "df", "-B1", "/").Output()
	if err != nil {
		log.Printf("warning: disk space check failed: %v (continuing anyway)", err)
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		log.Printf("warning: unexpected df output: %s", string(out))
		return nil
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		log.Printf("warning: cannot parse df output: %s", lines[1])
		return nil
	}
	availBytes, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		log.Printf("warning: cannot parse available bytes: %s", fields[3])
		return nil
	}
	totalBytes, _ := strconv.ParseInt(fields[1], 10, 64)
	usedBytes, _ := strconv.ParseInt(fields[2], 10, 64)
	availGB := float64(availBytes) / (1024 * 1024 * 1024)
	totalGB := float64(totalBytes) / (1024 * 1024 * 1024)
	usedGB := float64(usedBytes) / (1024 * 1024 * 1024)
	log.Printf("Docker VM disk: %.1f GB total, %.1f GB used, %.1f GB available", totalGB, usedGB, availGB)

	// Show docker system df summary breakdown
	dfOut, err := osexec.Command("docker", "system", "df").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(dfOut)), "\n") {
			log.Printf("  %s", line)
		}
	}

	// Show containers holding images (these block prune)
	psOut, err := osexec.Command("docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Size}}\t{{.Status}}").Output()
	if err == nil {
		containers := strings.Split(strings.TrimSpace(string(psOut)), "\n")
		if len(containers) > 0 && containers[0] != "" {
			log.Printf("  Running containers (block image/volume prune):")
			for _, c := range containers {
				log.Printf("    %s", c)
			}
		}
	}

	// Show per-volume sizes
	volOut, err := osexec.Command("docker", "system", "df", "-v").Output()
	if err == nil {
		inVolumes := false
		for _, line := range strings.Split(string(volOut), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(line, "Local Volumes") {
				inVolumes = true
				log.Printf("  Volumes:")
				continue
			}
			if inVolumes && strings.Contains(line, "Build cache") {
				break
			}
			if inVolumes && trimmed != "" && !strings.HasPrefix(trimmed, "VOLUME") {
				log.Printf("    %s", trimmed)
			}
		}
	}

	if availGB < float64(minFreeDiskGB()) {
		return fmt.Errorf("insufficient Docker VM disk: %.1f GB available, need %d GB\n"+
			"  To free space:\n"+
			"  1. Stop unused containers: docker stop <name>\n"+
			"  2. Then prune: docker system prune -a --volumes -f\n"+
			"  3. Or increase Docker Desktop VM disk: Settings → Resources → Virtual disk limit",
			availGB, minFreeDiskGB())
	}
	return nil
}

// TestMain cleans up locally-built test images after all tests complete.
//
// Long-test bucketing: when DEVCELL_TEST_BUILD_THIN=1, builds the ultimate-thin
// image ONCE before any test runs and exports its tag via DEVCELL_TEST_THIN_IMAGE
// so every thin-variant test reuses the same fresh image. Without that env var,
// tests fall back to a preexisting `devcell-user:ultimate-thin` or skip cleanly.
// This keeps the convention: short tests never trigger a build; long tests opt
// in via env var or `testing.Short()` gates.
func TestMain(m *testing.M) {
	if err := checkDiskSpace(); err != nil {
		log.Fatalf("disk space check failed: %v", err)
	}
	if os.Getenv("DEVCELL_TEST_BUILD_THIN") == "1" {
		tag, err := buildLocalImage("ultimate-thin", "devcell-user")
		if err != nil {
			log.Fatalf("DEVCELL_TEST_BUILD_THIN=1: build failed: %v", err)
		}
		log.Printf("DEVCELL_TEST_BUILD_THIN=1: built %s, exporting DEVCELL_TEST_THIN_IMAGE", tag)
		os.Setenv("DEVCELL_TEST_THIN_IMAGE", tag)
	}
	code := m.Run()
	if ultimateTag != "" {
		osexec.Command("docker", "rmi", ultimateTag).Run()
	}
	if baseTag != "" {
		osexec.Command("docker", "rmi", baseTag).Run()
	}
	if electronicsTag != "" {
		osexec.Command("docker", "rmi", electronicsTag).Run()
	}
	if testdataTag != "" {
		osexec.Command("docker", "rmi", testdataTag).Run()
	}
	os.Exit(code)
}

// shortSHA returns the abbreviated commit hash of HEAD.
// Falls back to a timestamp if git is unavailable (e.g. broken system gitconfig).
func shortSHA() string {
	cmd := osexec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("dev%s", time.Now().Format("150405"))
	}
	return strings.TrimSpace(string(out))
}

// buildLocalImage builds a bake target with a unique tag and returns the tag.
// THICK image path — produces a Dockerfile-built (impure) image. Use
// buildThinImage for thin variants where nix store lives on a volume.
//
// Pins PLATFORMS to the host arch because `docker buildx bake --load` can't
// import multi-platform manifests (docker-bake.hcl defaults to amd64+arm64).
func buildLocalImage(target, tagPrefix string) (string, error) {
	tag := fmt.Sprintf("%s:%s-%s", tagPrefix, shortSHA(), time.Now().Format("20060102T150405"))
	hostPlatform := "linux/" + runtime.GOARCH // GOARCH already uses Docker's vocabulary (amd64, arm64)
	log.Printf("Building %s image: %s (platform=%s)", target, tag, hostPlatform)
	cmd := osexec.Command("docker", "buildx", "bake",
		"--file", "docker-bake.hcl",
		"--load",
		"--set", fmt.Sprintf("%s.tags=%s", target, tag),
		"--set", fmt.Sprintf("%s.platform=%s", target, hostPlatform),
		target)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "PLATFORMS="+hostPlatform)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build %s: %w", target, err)
	}
	return tag, nil
}

// buildThinImage invokes `cell build --thin --stack <stack> --image <tag>` to
// produce a thin image (nix store on /nix volume). Builds `bin/cell-test` on
// demand if not present. Returns the tag handed to --image. Honest E2E of the
// user-facing thin-build path; reuses the shared `devcell-nix-store` volume
// for incremental builds (~minutes when the store already has overlap).
func buildThinImage(stack string) (string, error) {
	cellBin, err := ensureCellBinary()
	if err != nil {
		return "", err
	}
	tag := fmt.Sprintf("devcell-user:%s-thin-%s", stack, shortSHA())
	log.Printf("Building thin image: stack=%s, tag=%s", stack, tag)
	cmd := osexec.Command(cellBin, "build", "--thin", "--stack", stack, "--image", tag, "--debug")
	cmd.Dir = ".."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cell build --thin --stack %s --image %s: %w", stack, tag, err)
	}
	return tag, nil
}

// ensureCellBinary builds ../bin/cell-test ONCE per test process and returns
// its absolute path. Always rebuilds on first call (no stale-cache silent
// failures when iterating on cmd/* code); cached via sync.Once across tests
// in the same `go test` run.
func ensureCellBinary() (string, error) {
	cellBinOnce.Do(func() {
		// Write under TempDir to dodge race with cell-test binary held open by
		// other long-running processes (we keep multiple devcell shells alive
		// during dev). Per-run binary is fresh + isolated.
		tmp, err := os.MkdirTemp("", "cell-test-bin-")
		if err != nil {
			cellBinErr = fmt.Errorf("mkdir tmp: %w", err)
			return
		}
		cellBin := filepath.Join(tmp, "cell-test")
		log.Printf("Building cell binary at %s", cellBin)
		build := osexec.Command("go", "build", "-o", cellBin, "./cmd")
		build.Dir = ".."
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			cellBinErr = fmt.Errorf("build cell binary: %w", err)
			return
		}
		cellBinPath = cellBin
	})
	return cellBinPath, cellBinErr
}

var (
	cellBinOnce sync.Once
	cellBinPath string
	cellBinErr  error
)

// Local tag conventions (CELL-108):
//   - `task image:impure:build:ultimate` → ghcr.io/devcell-sh/devcell:ultimate-local
//   - `task image:pure:build:ultimate`   → devcell-user:ultimate-pure
const (
	localImpureUltimateTag = "ghcr.io/devcell-sh/devcell:ultimate-local"
	localPureUltimateTag   = "devcell-user:ultimate-pure"
	localThinUltimateTag   = "devcell-user:ultimate-thin"
	defaultThinVolumeName  = "devcell-nix-store"
)

// thinVolumeName returns the Docker volume to mount at /nix for thin-variant
// tests. Reads BOTH:
//   - DEVCELL_NIX_VOLUME (the canonical production env var also honoured by
//     `runner.ThinStoreVolume` — set by tests that drive `cell build` and
//     need build + run to target the same volume), preferred.
//   - DEVCELL_TEST_VOLUME_NAME (legacy test-only override), fallback.
// Either lets a test isolate to a unique volume with t.Cleanup-based removal.
func thinVolumeName() string {
	if v := os.Getenv("DEVCELL_NIX_VOLUME"); v != "" {
		return v
	}
	if v := os.Getenv("DEVCELL_TEST_VOLUME_NAME"); v != "" {
		return v
	}
	return defaultThinVolumeName
}

// imageTagForVariant resolves the test image tag for a given variant
// without touching docker — pure function so the priority order is
// table-testable (CELL-108).
//
// Returns (tag, skipReason). Caller semantics:
//   - tag != "" → use it directly.
//   - tag == "" && skipReason == "" → caller falls back to its variant-specific
//     default (e.g. scratch bake for impure).
//   - tag == "" && skipReason != "" → caller should t.Skip(skipReason) or panic.
func imageTagForVariant(variant, pureEnv, impureEnv string, exists func(string) bool) (tag, skipReason string) {
	switch variant {
	case "thin":
		if env := os.Getenv("DEVCELL_TEST_THIN_IMAGE"); env != "" {
			return env, ""
		}
		if exists(localThinUltimateTag) {
			return localThinUltimateTag, ""
		}
		return "", "thin variant requested but local `" + localThinUltimateTag + "` is not available; run `cell build --thin` to enable"
	case "pure":
		if pureEnv != "" {
			return pureEnv, ""
		}
		if exists(localPureUltimateTag) {
			return localPureUltimateTag, ""
		}
		return "", "pure variant requested but neither DEVCELL_TEST_PURE_IMAGE nor local `" + localPureUltimateTag + "` is available; run `task image:pure:build:ultimate` to enable"
	case "impure", "":
		if impureEnv != "" {
			return impureEnv, ""
		}
		if exists(localImpureUltimateTag) {
			return localImpureUltimateTag, ""
		}
		return "", "" // caller falls back to scratch bake
	default:
		return "", "unknown DEVCELL_TEST_VARIANT: " + variant
	}
}

// image returns the test image tag for the default variant (impure unless
// DEVCELL_TEST_VARIANT=pure). Priority within a variant:
//  1. Variant-specific env override (DEVCELL_TEST_PURE_IMAGE for pure,
//     DEVCELL_TEST_IMAGE for impure)
//  2. Locally-loaded variant tag (devcell-user:ultimate-pure or ultimate-local)
//  3. Impure only: build from testdata Dockerfile on top of ultimate-local;
//     or, last resort, scratch-bake `local-ultimate` (~10 min)
//  4. Pure only: panic with a setup hint — no automatic bake for pure
//     because nix2container builds are not safe to run unattended from go test
func image() string {
	variant := os.Getenv("DEVCELL_TEST_VARIANT")
	tag, skip := imageTagForVariant(
		variant,
		os.Getenv("DEVCELL_TEST_PURE_IMAGE"),
		os.Getenv("DEVCELL_TEST_IMAGE"),
		imageExists,
	)
	if tag != "" {
		// For impure local tag, route through the testdata Dockerfile path so
		// nixhome/ changes are picked up on every test run (~53s). Env overrides
		// and pure tags are used as-is.
		if tag == localImpureUltimateTag && os.Getenv("DEVCELL_TEST_IMAGE") == "" {
			return testdataImage()
		}
		return tag
	}
	if skip != "" {
		// `image()` can't t.Skip — it has no *testing.T. Panic with a clear
		// setup hint. Tests that need a graceful skip should call pureImage(t)
		// or impureImage(t) directly.
		panic("image: " + skip)
	}
	// Empty tag + no skip = impure scratch-bake fallback.
	ultimateOnce.Do(func() {
		ultimateTag, ultimateErr = buildLocalImage("local-ultimate", "devcell-test")
	})
	if ultimateErr != nil {
		panic(fmt.Sprintf("image: %v", ultimateErr))
	}
	return ultimateTag
}

// pureImage returns the pure (nix2container) variant tag for tests asserting
// pure-image-specific behavior. Skips the test if no pure image is available
// (env override or local tag from `task image:pure:build:ultimate`).
func pureImage(t *testing.T) string {
	t.Helper()
	tag, skip := imageTagForVariant(
		"pure",
		os.Getenv("DEVCELL_TEST_PURE_IMAGE"),
		"",
		imageExists,
	)
	if tag == "" {
		t.Skip(skip)
	}
	return tag
}

// impureImage returns the impure (Debian-based) variant tag explicitly,
// bypassing DEVCELL_TEST_VARIANT. Useful for tests that assert
// impure-specific behavior (e.g. /etc/devcell/base-image-version).
func impureImage(t *testing.T) string {
	t.Helper()
	tag, _ := imageTagForVariant(
		"impure",
		"",
		os.Getenv("DEVCELL_TEST_IMAGE"),
		imageExists,
	)
	if tag != "" && tag == localImpureUltimateTag && os.Getenv("DEVCELL_TEST_IMAGE") == "" {
		return testdataImage()
	}
	if tag != "" {
		return tag
	}
	// Fall back to the same scratch-bake path image() uses for impure.
	ultimateOnce.Do(func() {
		ultimateTag, ultimateErr = buildLocalImage("local-ultimate", "devcell-test")
	})
	if ultimateErr != nil {
		t.Fatalf("impureImage: %v", ultimateErr)
	}
	return ultimateTag
}

// imageExists checks if a Docker image exists locally.
func imageExists(tag string) bool {
	return osexec.Command("docker", "image", "inspect", tag).Run() == nil
}

// baseImage returns the core image tag for entrypoint tests.
// Uses DEVCELL_TEST_BASE_IMAGE if set (CI); otherwise builds local-core once with a unique tag.
func baseImage() string {
	if img := os.Getenv("DEVCELL_TEST_BASE_IMAGE"); img != "" {
		return img
	}
	baseOnce.Do(func() {
		baseTag, baseErr = buildLocalImage("local-core", "devcell-test-base")
	})
	if baseErr != nil {
		panic(fmt.Sprintf("baseImage: %v", baseErr))
	}
	return baseTag
}

// ── Electronics image (base + home-manager switch devcell-electronics) ────────
//
// Builds a user-level image following the scaffold Dockerfile pattern:
//   1. FROM base image (nix + home-manager, no stack)
//   2. Copy local nixhome/ flake
//   3. home-manager switch --flake .#devcell-electronics (smallest profile with desktop module)
//   4. patchright now comes from nix (scraping/default.nix buildNpmPackage), not npm
//
// Used by stealth MCP tests instead of the pre-built ultimate image.

const elecDockerfile = `FROM {{BASE_IMAGE}}

COPY --chown=devcell:usergroup nixhome/ /opt/devcell/.config/devcell/nixhome/
COPY --chown=devcell:usergroup flake.nix /opt/devcell/.config/devcell/

RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch \
      --flake "/opt/devcell/.config/devcell#devcell-electronics${ARCH_SUFFIX}" \
      --impure && \
    ln -sfT "$(readlink -f /opt/devcell/.nix-profile)" \
            /opt/devcell/.local/state/nix/profiles/profile

COPY --chown=devcell:usergroup package.json /opt/npm-tools/
RUN cd /opt/npm-tools && npm install
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"
`

const elecFlakeNix = `{
  description = "DevCell electronics test stack";
  inputs.devcell.url = "path:./nixhome";
  outputs = { self, devcell, ... }: {
    homeConfigurations = devcell.homeConfigurations;
  };
}
`

const elecPackageJSON = `{
  "name": "devcell-tools",
  "version": "1.0.0",
  "private": true,
  "dependencies": {}
}
`

// buildElectronicsImage creates a temp build context with the local nixhome,
// writes a Dockerfile targeting devcell-electronics, and runs docker build.
func buildElectronicsImage() (string, error) {
	baseImg := baseImage()

	dir, err := os.MkdirTemp("", "devcell-elec-test-*")
	if err != nil {
		return "", fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	// Write Dockerfile with base image substituted.
	dockerfile := strings.ReplaceAll(elecDockerfile, "{{BASE_IMAGE}}", baseImg)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return "", fmt.Errorf("write Dockerfile: %w", err)
	}

	// Write flake.nix (path:./nixhome input).
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(elecFlakeNix), 0644); err != nil {
		return "", fmt.Errorf("write flake.nix: %w", err)
	}

	// Write package.json (only patchright-mcp).
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(elecPackageJSON), 0644); err != nil {
		return "", fmt.Errorf("write package.json: %w", err)
	}

	// Copy local nixhome/ into the build context.
	nixhomeSrc := filepath.Join("..", "nixhome")
	nixhomeDst := filepath.Join(dir, "nixhome")
	if err := copyDirRecursive(nixhomeSrc, nixhomeDst); err != nil {
		return "", fmt.Errorf("copy nixhome: %w", err)
	}

	tag := fmt.Sprintf("devcell-test-electronics:%s-%s", shortSHA(), time.Now().Format("20060102T150405"))
	log.Printf("Building electronics image: %s (from base %s)", tag, baseImg)
	cmd := osexec.Command("docker", "build", "--no-cache", "--progress=plain", "-t", tag, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build electronics: %w", err)
	}
	return tag, nil
}

// copyDirRecursive copies src directory tree to dst.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// electronicsImage returns the electronics image tag.
// Uses DEVCELL_TEST_ELECTRONICS_IMAGE if set (CI); otherwise builds once from
// base + local nixhome with devcell-electronics stack.
func electronicsImage() string {
	if img := os.Getenv("DEVCELL_TEST_ELECTRONICS_IMAGE"); img != "" {
		return img
	}
	electronicsOnce.Do(func() {
		electronicsTag, electronicsErr = buildElectronicsImage()
	})
	if electronicsErr != nil {
		panic(fmt.Sprintf("electronicsImage: %v", electronicsErr))
	}
	return electronicsTag
}

// startElectronicsContainer starts a container from the electronics image.
func startElectronicsContainer(t *testing.T, env map[string]string) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: electronicsImage(),
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
		t.Fatalf("start electronics container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

// startElectronicsEnvContainer starts an electronics container with standard env.
func startElectronicsEnvContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	return startElectronicsContainer(t, map[string]string{
		"HOST_USER": hostUser,
		"APP_NAME":  "test",
	})
}

// ── Testdata image (ultimate-local + home-manager switch with local nix cache) ─
//
// Builds from the testdata Dockerfile which:
//   1. FROM ultimate-local (pre-built base with full nix store)
//   2. Copies testdata config + current nixhome/ into the image
//   3. home-manager switch using local /nix store as substituter (fast, no network)
//   4. Installs mise runtimes, npm tools, python tools
//
// This is the correct image for iterating on nixhome changes — it re-applies
// home-manager on top of the cached nix store, so config changes are tested.

const testdataDir = "testdata/devcell-config-simple/devcell"

// testRunDir returns the per-run results directory, creating it on first call.
// Layout: test/results/<YYYYMMDD-HHMMSS>-<sha>/
// When running inside a devcell container (Docker-in-Docker), the path is
// resolved to the host filesystem so Docker on the host can mount it.
func testRunDir() string {
	runDirOnce.Do(func() {
		ts := time.Now().Format("20060102-150405")
		runDir = filepath.Join(hostProjectPath("results"), ts+"-"+shortSHA())
		if err := os.MkdirAll(runDir, 0755); err != nil {
			panic(fmt.Sprintf("create run dir: %v", err))
		}
		log.Printf("Test run dir: %s", runDir)
	})
	return runDir
}

// hostProjectPath returns a path under the project's test/ directory that is
// accessible to both the test process and the host Docker daemon.
// Inside a devcell container, /devcell-68 is bind-mounted from the host — we
// read /proc/1/mountinfo to discover the host path so Docker can mount it.
// On CI or bare hosts, returns the relative path unchanged.
func hostProjectPath(rel string) string {
	if dir := os.Getenv("DEVCELL_TEST_PROJECT_DIR"); dir != "" {
		return filepath.Join(dir, rel)
	}
	// Detect devcell container by checking if /devcell-68 mount exists in mountinfo.
	if data, err := os.ReadFile("/proc/1/mountinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			// Example: 511 477 0:44 /dmitry/dev/dimmkirr/devcell /devcell-68 rw,...- fakeowner /run/host_mark/Users rw,...
			if strings.Contains(line, " /devcell-68 ") || strings.Contains(line, " "+os.Getenv("WORKSPACE")+" ") {
				fields := strings.Fields(line)
				if len(fields) >= 4 {
					// fields[3] = mount source relative path (e.g. /dmitry/dev/dimmkirr/devcell)
					// Find the filesystem root after the " - " separator
					for i, f := range fields {
						if f == "-" && i+2 < len(fields) {
							fsRoot := fields[i+2] // e.g. /run/host_mark/Users
							// macOS Docker: /run/host_mark/Users → /Users
							hostRoot := strings.TrimPrefix(fsRoot, "/run/host_mark")
							hostPath := filepath.Join(hostRoot, fields[3], "test", rel)
							return hostPath
						}
					}
				}
			}
		}
	}
	return rel
}

// buildTestdataImage builds from the testdata Dockerfile with current nixhome.
// The build context is persisted in testRunDir()/build-context/ for inspection.
func buildTestdataImage() (string, error) {
	dir := filepath.Join(testRunDir(), "build-context")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir build-context: %w", err)
	}

	// Copy testdata build context.
	if err := copyDirRecursive(testdataDir, dir); err != nil {
		return "", fmt.Errorf("copy testdata: %w", err)
	}

	// Replace testdata nixhome with current repo nixhome for iteration.
	nixhomeDst := filepath.Join(dir, "nixhome")
	os.RemoveAll(nixhomeDst)
	if err := copyDirRecursive(filepath.Join("..", "nixhome"), nixhomeDst); err != nil {
		return "", fmt.Errorf("copy nixhome: %w", err)
	}

	tag := fmt.Sprintf("devcell-test-testdata:%s-%s", shortSHA(), time.Now().Format("20060102T150405"))
	log.Printf("Building testdata image: %s", tag)
	cmd := osexec.Command("docker", "build", "--progress=plain", "-t", tag, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build testdata: %w", err)
	}
	return tag, nil
}

// testdataImage returns the testdata image tag.
// Uses DEVCELL_TEST_TESTDATA_IMAGE if set; otherwise builds once from
// ultimate-local + current nixhome via home-manager switch.
func testdataImage() string {
	if img := os.Getenv("DEVCELL_TEST_TESTDATA_IMAGE"); img != "" {
		return img
	}
	testdataOnce.Do(func() {
		testdataTag, testdataErr = buildTestdataImage()
	})
	if testdataErr != nil {
		panic(fmt.Sprintf("testdataImage: %v", testdataErr))
	}
	return testdataTag
}

// isThinVariant returns true when running integration tests against the thin image.
func isThinVariant() bool {
	return os.Getenv("DEVCELL_TEST_VARIANT") == "thin"
}

func startContainer(t *testing.T, env map[string]string) testcontainers.Container {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: image(),
		Env:   env,
		User:  "0", // entrypoint.sh starts as root, drops via gosu
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * 1e9),
	}

	// Thin variant: mount the nix store volume
	if isThinVariant() {
		req.Mounts = testcontainers.Mounts(
			testcontainers.VolumeMount(thinVolumeName(), "/nix"),
		)
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

func exec(t *testing.T, c testcontainers.Container, cmd []string) (string, int) {
	t.Helper()
	ctx := context.Background()
	code, reader, err := c.Exec(ctx, cmd)
	if err != nil {
		t.Fatalf("exec %v: %v", cmd, err)
	}
	var stdout bytes.Buffer
	stdcopy.StdCopy(&stdout, io.Discard, reader)
	return strings.TrimSpace(stdout.String()), code
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
