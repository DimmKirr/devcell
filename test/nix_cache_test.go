// nix_cache_test.go — TDD tests for DIMM-118: nix store pre-seeding with DB
//
// L1: Dockerfile/bake syntax validation (no Docker needed)
// L2: Integration — nix DB recognized after copy (needs Docker)
// L3: E2E — full build with cache donor (needs Docker + registry)

package container_test

import (
	"os"
	osexec "os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// L1 — Unit: Dockerfile syntax & mount correctness
// ---------------------------------------------------------------------------

// TestDockerfile_NixCacheMount_HasVarNix asserts the ultimate stage mounts
// /nix/var/nix from the nix-cache stage alongside /nix/store.
func TestDockerfile_NixCacheMount_HasVarNix(t *testing.T) {
	dockerfile := readDockerfile(t)

	// Find the ultimate stage's RUN step that does the nix-cache mount.
	// It should have TWO --mount directives: one for /nix/store, one for /nix/var/nix.
	ultimateRUN := extractUltimateNixCacheRUN(t, dockerfile)

	if !strings.Contains(ultimateRUN, "--mount=from=nix-cache,source=/nix/store") {
		t.Fatal("ultimate RUN missing --mount for /nix/store")
	}
	if !strings.Contains(ultimateRUN, "--mount=from=nix-cache,source=/nix/var/nix") {
		t.Fatal("ultimate RUN missing --mount for /nix/var/nix (nix DB)")
	}

	// Verify both cp commands exist.
	if !strings.Contains(ultimateRUN, "cp -a /tmp/nix-cache/. /nix/store/") {
		t.Fatal("ultimate RUN missing 'cp -a' for /nix/store")
	}
	if !strings.Contains(ultimateRUN, "cp -a /tmp/nix-var-cache/. /nix/var/nix/") {
		t.Fatal("ultimate RUN missing 'cp -a' for /nix/var/nix")
	}
	t.Log("PASS: ultimate stage mounts and copies both /nix/store and /nix/var/nix")
}

// TestDockerfile_NixCacheStage_MkdirBoth asserts the nix-cache stage creates
// both /nix/store and /nix/var/nix directories.
func TestDockerfile_NixCacheStage_MkdirBoth(t *testing.T) {
	dockerfile := readDockerfile(t)

	// Find the nix-cache stage.
	nixCacheStage := extractStage(t, dockerfile, "nix-cache")

	if !strings.Contains(nixCacheStage, "/nix/store") {
		t.Fatal("nix-cache stage missing mkdir for /nix/store")
	}
	if !strings.Contains(nixCacheStage, "/nix/var/nix") {
		t.Fatal("nix-cache stage missing mkdir for /nix/var/nix")
	}
	t.Log("PASS: nix-cache stage creates both /nix/store and /nix/var/nix")
}

// TestBakeHCL_NixCacheImage_Variable asserts docker-bake.hcl declares
// NIX_CACHE_IMAGE and the ultimate target passes it as a build arg.
func TestBakeHCL_NixCacheImage_Variable(t *testing.T) {
	bake, err := os.ReadFile("../docker-bake.hcl")
	if err != nil {
		t.Fatalf("read docker-bake.hcl: %v", err)
	}
	content := string(bake)

	if !strings.Contains(content, `variable "NIX_CACHE_IMAGE"`) {
		t.Fatal("docker-bake.hcl missing NIX_CACHE_IMAGE variable")
	}

	// ultimate target should pass NIX_CACHE_IMAGE as arg.
	ultimateTarget := extractBakeTarget(t, content, "ultimate")
	if !strings.Contains(ultimateTarget, "NIX_CACHE_IMAGE") {
		t.Fatal("ultimate bake target doesn't pass NIX_CACHE_IMAGE as arg")
	}
	t.Log("PASS: docker-bake.hcl has NIX_CACHE_IMAGE variable and ultimate target uses it")
}

// ---------------------------------------------------------------------------
// L2 — Integration: nix DB recognized after copy
// ---------------------------------------------------------------------------

// TestNixCache_DbPresent verifies the ultimate image has a valid nix DB
// with registered store paths. If the DB is missing or empty, nix would
// re-download everything on next home-manager switch.
func TestNixCache_DbPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := image()

	// Verify /nix/var/nix/db/db.sqlite exists and is non-empty.
	out, err := osexec.Command("docker", "run", "--rm",
		"--entrypoint", "bash",
		img,
		"-c", "test -f /nix/var/nix/db/db.sqlite && stat -c %s /nix/var/nix/db/db.sqlite",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("nix DB check failed: %v\noutput: %s", err, out)
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse DB size: %v (output: %s)", err, out)
	}
	if size < 1024 {
		t.Fatalf("nix DB suspiciously small: %d bytes", size)
	}
	t.Logf("PASS: nix DB exists, %d bytes", size)
}

// TestNixCache_PathsRegistered verifies nix knows about store paths
// (they're registered in the DB, not just files on disk).
func TestNixCache_PathsRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := image()

	out, err := osexec.Command("docker", "run", "--rm",
		"--entrypoint", "bash",
		img,
		"-lc", "nix path-info --all 2>/dev/null | wc -l",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("nix path-info failed: %v\noutput: %s", err, out)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse path count: %v (output: %s)", err, out)
	}
	// A working ultimate image should have hundreds of registered paths.
	if count < 100 {
		t.Fatalf("only %d nix paths registered — DB likely not pre-seeded", count)
	}
	t.Logf("PASS: %d nix paths registered in DB", count)
}

// ---------------------------------------------------------------------------
// L3 — E2E: built image has expected tools
// ---------------------------------------------------------------------------

// TestNixCache_UltimateTools verifies key tools are present in the
// ultimate image — this confirms the full build (with or without cache)
// produced a working image.
func TestNixCache_UltimateTools(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	img := image()

	tools := []struct {
		name string
		cmd  string
	}{
		{"nix", "nix --version"},
		{"home-manager", "home-manager --version"},
		{"claude", "claude --version"},
		{"node", "node --version"},
		{"go", "go version"},
	}

	for _, tc := range tools {
		t.Run(tc.name, func(t *testing.T) {
			// Use -c (not -lc) — login shell may reset PATH, but Docker ENV
			// already has /opt/mise/*/bin on PATH for mise-installed tools.
			out, err := osexec.Command("docker", "run", "--rm",
				"--entrypoint", "bash",
				img,
				"-c", tc.cmd,
			).CombinedOutput()
			if err != nil {
				t.Fatalf("%s not available: %v\noutput: %s", tc.name, err, out)
			}
			t.Logf("PASS: %s → %s", tc.name, strings.TrimSpace(string(out)))
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func readDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../images/Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	return string(data)
}

// extractUltimateNixCacheRUN finds the RUN instruction in the ultimate stage
// that does the nix-cache mount (contains "nix-cache" and "home-manager switch").
// Dockerfile RUN instructions can span multiple lines with backslash continuation.
func extractUltimateNixCacheRUN(t *testing.T, dockerfile string) string {
	t.Helper()
	ultimateStage := extractStage(t, dockerfile, "ultimate")

	// Find RUN lines that reference nix-cache mount.
	lines := strings.Split(ultimateStage, "\n")
	var run strings.Builder
	inRun := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "RUN") && strings.Contains(trimmed, "nix-cache") {
			inRun = true
		}
		if inRun {
			run.WriteString(line)
			run.WriteString("\n")
			if !strings.HasSuffix(trimmed, "\\") {
				break
			}
		}
	}
	result := run.String()
	if result == "" {
		t.Fatal("could not find RUN with nix-cache mount in ultimate stage")
	}
	return result
}

// extractStage extracts a named stage from a Dockerfile (FROM ... AS <name>
// through the next FROM or EOF).
func extractStage(t *testing.T, dockerfile, name string) string {
	t.Helper()
	// Match "FROM ... AS <name>" case-insensitively.
	pattern := regexp.MustCompile(`(?im)^FROM\s+.+\s+AS\s+` + regexp.QuoteMeta(name) + `\s*$`)
	loc := pattern.FindStringIndex(dockerfile)
	if loc == nil {
		t.Fatalf("stage %q not found in Dockerfile", name)
	}

	rest := dockerfile[loc[0]:]
	// Find next FROM (start of next stage) or EOF.
	nextFROM := regexp.MustCompile(`(?im)^FROM\s+`)
	locs := nextFROM.FindAllStringIndex(rest, 2)
	if len(locs) < 2 {
		return rest // Last stage.
	}
	return rest[:locs[1][0]]
}

// extractBakeTarget extracts a target block from docker-bake.hcl by name.
func extractBakeTarget(t *testing.T, content, name string) string {
	t.Helper()
	// Find `target "<name>" {` and extract until matching `}`.
	pattern := `target "` + name + `"`
	idx := strings.Index(content, pattern)
	if idx == -1 {
		t.Fatalf("bake target %q not found", name)
	}
	// Find opening brace.
	braceStart := strings.Index(content[idx:], "{")
	if braceStart == -1 {
		t.Fatalf("no opening brace for target %q", name)
	}
	start := idx + braceStart
	depth := 0
	for i := start; i < len(content); i++ {
		if content[i] == '{' {
			depth++
		} else if content[i] == '}' {
			depth--
			if depth == 0 {
				return content[idx : i+1]
			}
		}
	}
	t.Fatalf("unmatched braces in target %q", name)
	return ""
}
