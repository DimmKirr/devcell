package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// ThinStoreVolume is the single source of truth for the named Docker volume
// holding the thin-mode /nix store. Default is "devcell-nix-store"; override
// via DEVCELL_NIX_VOLUME (parallel test runs, isolated CI, dual installations).

func TestThinStoreVolume_Default(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "")
	if got := runner.ThinStoreVolume(); got != "devcell-nix-store" {
		t.Errorf("default: got %q, want devcell-nix-store", got)
	}
}

func TestThinStoreVolume_EnvOverride(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "my-test-vol")
	if got := runner.ThinStoreVolume(); got != "my-test-vol" {
		t.Errorf("env override: got %q, want my-test-vol", got)
	}
}

func TestThinStoreVolume_EmptyEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "  ")
	if got := runner.ThinStoreVolume(); got != "devcell-nix-store" {
		t.Errorf("whitespace-only env should be treated as unset, got %q", got)
	}
}
