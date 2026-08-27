package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// UpstreamFlakeRef is the single builder for the canonical nixhome flake URL.
// Replaces 4 scattered fmt.Sprintf calls that all encoded the same template.

func TestUpstreamFlakeRef_ExplicitVersion(t *testing.T) {
	if got := runner.UpstreamFlakeRef("v1.2.3"); got != "github:devcell-sh/community-home/v1.2.3" {
		t.Errorf("got %q", got)
	}
}

func TestUpstreamFlakeRef_EmptyCoercesToDefault(t *testing.T) {
	want := "github:devcell-sh/community-home/" + runner.DefaultNixhomeGitRef
	if got := runner.UpstreamFlakeRef(""); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamFlakeRef_V000CoercesToDefault(t *testing.T) {
	want := "github:devcell-sh/community-home/" + runner.DefaultNixhomeGitRef
	if got := runner.UpstreamFlakeRef("v0.0.0"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamFlakeRef_DevVersionCoercesToDefault(t *testing.T) {
	want := "github:devcell-sh/community-home/" + runner.DefaultNixhomeGitRef
	for _, v := range []string{
		"v0.8.2-94-g0ac6be1-dirty",
		"v1.0.0-3-gabcdef0",
		"v2.0.0-dirty",
	} {
		if got := runner.UpstreamFlakeRef(v); got != want {
			t.Errorf("UpstreamFlakeRef(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestResolveNixhomeRef_EnvOverride(t *testing.T) {
	t.Setenv("DEVCELL_NIXHOME", "/home/user/my-nixhome")
	if got := runner.ResolveNixhomeRef("v1.0.0"); got != "/home/user/my-nixhome" {
		t.Errorf("got %q, want env override", got)
	}
}

func TestResolveNixhomeRef_LegacyPathFallback(t *testing.T) {
	t.Setenv("DEVCELL_NIXHOME", "")
	t.Setenv("DEVCELL_NIXHOME_PATH", "/Users/me/dev/community-home")
	if got := runner.ResolveNixhomeRef("v1.0.0"); got != "/Users/me/dev/community-home" {
		t.Errorf("got %q, want legacy DEVCELL_NIXHOME_PATH fallback", got)
	}
}

func TestResolveNixhomeRef_NewOverridesLegacy(t *testing.T) {
	t.Setenv("DEVCELL_NIXHOME", "github:myuser/my-nixhome/dev")
	t.Setenv("DEVCELL_NIXHOME_PATH", "/Users/me/dev/community-home")
	if got := runner.ResolveNixhomeRef("v1.0.0"); got != "github:myuser/my-nixhome/dev" {
		t.Errorf("got %q, want DEVCELL_NIXHOME to take precedence", got)
	}
}

func TestResolveNixhomeRef_DefaultsToUpstream(t *testing.T) {
	t.Setenv("DEVCELL_NIXHOME", "")
	t.Setenv("DEVCELL_NIXHOME_PATH", "")
	want := runner.UpstreamFlakeRef("v1.0.0")
	if got := runner.ResolveNixhomeRef("v1.0.0"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamFlakeRefNoVersion(t *testing.T) {
	// The "no specific ref" variant — for callers that want the catalog as it
	// exists upstream today (not pinned to a version). Used by `cell modules
	// list` and similar introspection.
	if got := runner.UpstreamFlakeRefNoVersion(); got != "github:devcell-sh/community-home" {
		t.Errorf("got %q", got)
	}
}
