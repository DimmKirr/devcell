// nix_cache_test.go — TDD tests for CELL-163: nix store pre-seeding with DB
//
// L2: Integration — nix DB recognized after copy (needs Docker)
// L3: E2E — full build with cache donor (needs Docker + registry)

package container_test

import (
	osexec "os/exec"
	"strconv"
	"strings"
	"testing"
)

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
