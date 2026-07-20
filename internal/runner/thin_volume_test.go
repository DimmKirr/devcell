package runner_test

import (
	"runtime"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// ThinStoreVolume is the single source of truth for the named Docker volume
// holding the thin-mode /nix store. Default is "devcell-nix-store"; override
// via DEVCELL_NIX_VOLUME (parallel test runs, isolated CI, dual installations).
// When DEVCELL_ARCH requests a non-native architecture, the volume name gets
// an arch suffix so cross-arch builds use a separate nix store.

func TestThinStoreVolume_Default(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "")
	t.Setenv("DEVCELL_ARCH", "")
	if got := runner.ThinStoreVolume(); got != "devcell-nix-store" {
		t.Errorf("default: got %q, want devcell-nix-store", got)
	}
}

func TestThinStoreVolume_EnvOverride(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "my-test-vol")
	t.Setenv("DEVCELL_ARCH", "")
	if got := runner.ThinStoreVolume(); got != "my-test-vol" {
		t.Errorf("env override: got %q, want my-test-vol", got)
	}
}

func TestThinStoreVolume_EmptyEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "  ")
	t.Setenv("DEVCELL_ARCH", "")
	if got := runner.ThinStoreVolume(); got != "devcell-nix-store" {
		t.Errorf("whitespace-only env should be treated as unset, got %q", got)
	}
}

func TestThinStoreVolume_CrossArchGetsSuffix(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "")
	// Set DEVCELL_ARCH to the opposite of the host arch.
	if runtime.GOARCH == "arm64" {
		t.Setenv("DEVCELL_ARCH", "amd64")
		if got := runner.ThinStoreVolume(); got != "devcell-nix-store-amd64" {
			t.Errorf("cross-arch amd64 on arm64 host: got %q, want devcell-nix-store-amd64", got)
		}
	} else {
		t.Setenv("DEVCELL_ARCH", "arm64")
		if got := runner.ThinStoreVolume(); got != "devcell-nix-store-arm64" {
			t.Errorf("cross-arch arm64 on amd64 host: got %q, want devcell-nix-store-arm64", got)
		}
	}
}

func TestThinStoreVolume_NativeArchNoSuffix(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "")
	// Set DEVCELL_ARCH to the same as the host arch — should NOT add suffix.
	if runtime.GOARCH == "arm64" {
		t.Setenv("DEVCELL_ARCH", "arm64")
	} else {
		t.Setenv("DEVCELL_ARCH", "amd64")
	}
	if got := runner.ThinStoreVolume(); got != "devcell-nix-store" {
		t.Errorf("native arch should not add suffix: got %q, want devcell-nix-store", got)
	}
}

func TestThinStoreVolume_ExplicitVolumeOverridesTarchSuffix(t *testing.T) {
	t.Setenv("DEVCELL_NIX_VOLUME", "custom-vol")
	if runtime.GOARCH == "arm64" {
		t.Setenv("DEVCELL_ARCH", "amd64")
	} else {
		t.Setenv("DEVCELL_ARCH", "arm64")
	}
	if got := runner.ThinStoreVolume(); got != "custom-vol" {
		t.Errorf("explicit DEVCELL_NIX_VOLUME should override arch suffix: got %q, want custom-vol", got)
	}
}
