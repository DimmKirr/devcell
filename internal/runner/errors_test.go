package runner_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// TestTranslateError_DockerDaemonNotRunning: most common first-time failure.
func TestTranslateError_DockerDaemonNotRunning(t *testing.T) {
	raw := errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	got := runner.TranslateError(raw)
	if !strings.Contains(got, "Docker") {
		t.Errorf("expected mention of Docker, got: %s", got)
	}
	// Must include a next action (start/run)
	if !strings.Contains(strings.ToLower(got), "start") {
		t.Errorf("expected actionable suggestion ('start'), got: %s", got)
	}
}

// TestTranslateError_NixHashMismatch: transient network glitch fetching deps.
func TestTranslateError_NixHashMismatch(t *testing.T) {
	raw := errors.New("error: hash mismatch in fixed-output derivation '/nix/store/abc'")
	got := runner.TranslateError(raw)
	if !strings.Contains(strings.ToLower(got), "network") && !strings.Contains(strings.ToLower(got), "hash") {
		t.Errorf("expected mention of network/hash, got: %s", got)
	}
	if !strings.Contains(strings.ToLower(got), "re-run") && !strings.Contains(strings.ToLower(got), "retry") {
		t.Errorf("expected retry hint, got: %s", got)
	}
}

// TestTranslateError_NoSpaceLeft: out-of-disk during build.
func TestTranslateError_NoSpaceLeft(t *testing.T) {
	raw := errors.New("write /tmp/blob: no space left on device")
	got := runner.TranslateError(raw)
	if !strings.Contains(strings.ToLower(got), "disk") {
		t.Errorf("expected 'disk' in translated message, got: %s", got)
	}
	if !strings.Contains(got, "prune") {
		t.Errorf("expected 'prune' suggestion, got: %s", got)
	}
}

// TestTranslateError_PullAccessDenied: pre-built image not in registry.
func TestTranslateError_PullAccessDenied(t *testing.T) {
	raw := errors.New("pull access denied for example.com/foo, repository does not exist")
	got := runner.TranslateError(raw)
	if !strings.Contains(strings.ToLower(got), "image") {
		t.Errorf("expected mention of image, got: %s", got)
	}
}

// TestTranslateError_NixBuilderFailed: generic nix build failure.
func TestTranslateError_NixBuilderFailed(t *testing.T) {
	raw := errors.New("error: builder for '/nix/store/zzz.drv' failed with exit code 1")
	got := runner.TranslateError(raw)
	if !strings.Contains(strings.ToLower(got), "nix build") {
		t.Errorf("expected mention of nix build, got: %s", got)
	}
}

// TestTranslateError_AttributeMissing: typo in attribute reference (.devcellModules vs .modules).
func TestTranslateError_AttributeMissing(t *testing.T) {
	raw := errors.New("error: attribute 'devcelmodules' missing")
	got := runner.TranslateError(raw)
	if !strings.Contains(strings.ToLower(got), "attribute") {
		t.Errorf("expected mention of attribute, got: %s", got)
	}
}

// TestTranslateError_UnknownPassesThrough: unknown errors must NOT get wrapped
// (no false-positive translation). Returns the original message verbatim.
func TestTranslateError_UnknownPassesThrough(t *testing.T) {
	raw := errors.New("some weird error nobody has seen before")
	got := runner.TranslateError(raw)
	if got != raw.Error() {
		t.Errorf("unknown errors should pass through verbatim\n got:  %s\n want: %s", got, raw.Error())
	}
}

// TestTranslateError_NilSafe: defensive — nil error should not panic.
func TestTranslateError_NilSafe(t *testing.T) {
	got := runner.TranslateError(nil)
	if got != "" {
		t.Errorf("nil error should return empty string, got: %s", got)
	}
}
