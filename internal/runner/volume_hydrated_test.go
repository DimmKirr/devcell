package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// VolumeHydrated is the gate that CELL-38 widens cmd/root.go:312 with.
// Pure-function level: takes injectable probes so we don't need real docker
// in unit tests. Integration coverage lives in test/.
//
// Signature decision: VolumeHydrated(volumeName, sentinelPath, exists, probe) bool
//   - exists(volume) → bool        : "docker volume inspect" succeeded
//   - probe(volume, path) → bool   : sentinel file is present inside the volume

func TestVolumeHydrated_MissingVolumeReturnsFalse(t *testing.T) {
	exists := func(string) bool { return false }
	probe := func(string, string) bool {
		t.Fatal("probe should not run when volume is missing")
		return false
	}
	if runner.VolumeHydrated("devcell-nix-store", "/nix/var/nix/profiles/devcell-tools/bin/tini", exists, probe) {
		t.Error("missing volume must be unhydrated")
	}
}

func TestVolumeHydrated_PresentVolumeMissingSentinelReturnsFalse(t *testing.T) {
	exists := func(string) bool { return true }
	probe := func(string, string) bool { return false }
	if runner.VolumeHydrated("devcell-nix-store", "/nix/var/nix/profiles/devcell-tools/bin/tini", exists, probe) {
		t.Error("present-but-empty volume must be unhydrated")
	}
}

func TestVolumeHydrated_PresentVolumePresentSentinelReturnsTrue(t *testing.T) {
	exists := func(string) bool { return true }
	probe := func(vol, path string) bool {
		if vol != "devcell-nix-store" || path != "/nix/var/nix/profiles/devcell-tools/bin/tini" {
			t.Errorf("probe received unexpected args: vol=%q path=%q", vol, path)
		}
		return true
	}
	if !runner.VolumeHydrated("devcell-nix-store", "/nix/var/nix/profiles/devcell-tools/bin/tini", exists, probe) {
		t.Error("present volume with sentinel must be hydrated")
	}
}

// CELL-45: VolumeContainsArgv must mount the volume at /nix and probe the
// sentinel path verbatim. Mounting at /probe (the original implementation)
// breaks absolute /nix/store symlinks inside nix profiles — `test -e` follows
// the dangling symlink and returns false, causing spurious rebuilds.

func TestVolumeContainsArgv_MountsAtNix(t *testing.T) {
	argv := runner.VolumeContainsArgv("devcell-nix-store", "/nix/var/nix/profiles/devcell-tools/bin/tini")

	wantMount := "devcell-nix-store:/nix:ro"
	found := false
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && argv[i+1] == wantMount {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v %s in argv, got: %v", wantMount, argv)
	}
}

func TestVolumeContainsArgv_DoesNotRewriteSentinel(t *testing.T) {
	sentinel := "/nix/var/nix/profiles/devcell-tools/bin/tini"
	argv := runner.VolumeContainsArgv("devcell-nix-store", sentinel)

	// The literal sentinel path must appear in argv — no /probe rewrite.
	found := false
	for _, a := range argv {
		if a == sentinel {
			found = true
		}
		if strings.Contains(a, "/probe") {
			t.Errorf("argv must not reference /probe (CELL-45 regression): %v", argv)
		}
	}
	if !found {
		t.Errorf("expected literal sentinel %q in argv: %v", sentinel, argv)
	}
}
