package runner_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// PreflightNixBuilder extracts the macOS-needs-linux-builder check out of
// BuildImagePure's body so cmd/root.go can call it standalone to decide
// whether the host can usefully run a pure nix build. CELL-92.
//
// Returning a non-nil error means "do not invoke BuildImagePure on this
// host"; the caller treats this the same as "no nix on host" and falls
// through to ActionBuildImpure (docker build, nix runs inside).
//
// The diagnostic context (probe source, builders line, etc.) is wrapped
// into the returned error so the user still sees how to fix it.

func TestPreflightNixBuilder_LinuxHost_AlwaysOK(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test asserts Linux-host behavior; only runs on Linux")
	}
	if err := runner.PreflightNixBuilder("base"); err != nil {
		t.Errorf("Linux host should always pass preflight, got: %v", err)
	}
}

func TestPreflightNixBuilder_SkipEnv_AlwaysOK(t *testing.T) {
	t.Setenv("DEVCELL_PURE_SKIP_PREFLIGHT", "1")
	if err := runner.PreflightNixBuilder("base"); err != nil {
		t.Errorf("DEVCELL_PURE_SKIP_PREFLIGHT=1 should bypass, got: %v", err)
	}
}

func TestPreflightNixBuilder_DarwinNoBuilder_ReturnsActionableError(t *testing.T) {
	// Force darwin + no skip env, even when this test runs on Linux,
	// by invoking the testable inner via the exported seam. We use a
	// stub probe to drive the failure path deterministically.
	probe := runner.LinuxBuilderProbe{
		OK:           false,
		Source:       "nix-config-show",
		ConfigCmd:    "nix config show",
		BuildersLine: "",
	}
	err := runner.PreflightNixBuilderFromProbe(probe, "ultimate")
	if err == nil {
		t.Fatal("failed probe should return error")
	}
	for _, want := range []string{
		"linux-builder",        // tells user what's missing
		"DEVCELL_PURE_SKIP",    // tells user how to bypass
		"ultimate",             // mentions the stack
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q so user sees the fix path; got: %s", want, err.Error())
		}
	}
}
