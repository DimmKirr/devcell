package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// UpstreamFlakeRef is the single builder for the canonical nixhome flake URL.
// Replaces 4 scattered fmt.Sprintf calls that all encoded the same template.

func TestUpstreamFlakeRef_ExplicitVersion(t *testing.T) {
	if got := runner.UpstreamFlakeRef("v1.2.3"); got != "github:DimmKirr/devcell/v1.2.3?dir=nixhome" {
		t.Errorf("got %q", got)
	}
}

func TestUpstreamFlakeRef_EmptyCoercesToDefault(t *testing.T) {
	want := "github:DimmKirr/devcell/" + runner.DefaultNixhomeGitRef + "?dir=nixhome"
	if got := runner.UpstreamFlakeRef(""); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamFlakeRef_V000CoercesToDefault(t *testing.T) {
	want := "github:DimmKirr/devcell/" + runner.DefaultNixhomeGitRef + "?dir=nixhome"
	if got := runner.UpstreamFlakeRef("v0.0.0"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamFlakeRefNoVersion(t *testing.T) {
	// The "no specific ref" variant — for callers that want the catalog as it
	// exists upstream today (not pinned to a version). Used by `cell modules
	// list` and similar introspection.
	if got := runner.UpstreamFlakeRefNoVersion(); got != "github:DimmKirr/devcell?dir=nixhome" {
		t.Errorf("got %q", got)
	}
}
