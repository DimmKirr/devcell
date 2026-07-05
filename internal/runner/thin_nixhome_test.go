package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// Thin path mirrors pure path's 3-tier nixhome resolution (CELL-38):
//   1. TOML/env explicit
//   2. <BaseDir>/nixhome on disk
//   3. github:DimmKirr/devcell/<ver>?dir=nixhome fallback
//
// These tests pin the contract from the THIN caller's perspective —
// pure_nixhome_resolver_test.go covers the resolver itself, this file
// covers the thin path's reliance on the github fallback specifically
// (the gap CELL-38 closes — runBuildThin used to error out at tier 2).

func TestResolveThinNixhome_NoLocalFallsBackToGithub(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		Version:  "v1.2.3",
		StatFunc: func(string) error { return errNotFound{} },
	})
	if !got.Remote {
		t.Errorf("clean machine: want remote github fallback, got local: %+v", got)
	}
	want := "github:DimmKirr/devcell/v1.2.3?dir=nixhome"
	if got.FlakeRef != want {
		t.Errorf("FlakeRef: want %q, got %q", want, got.FlakeRef)
	}
}

func TestResolveThinNixhome_LocalNixhomeWinsOverGithub(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir:  "/project",
		Version:  "v1.2.3",
		StatFunc: func(string) error { return nil }, // local exists
	})
	if got.Remote {
		t.Errorf("local nixhome present: want path resolver, got remote: %+v", got)
	}
	if got.LocalPath != "/project/nixhome" {
		t.Errorf("LocalPath: want /project/nixhome, got %q", got.LocalPath)
	}
}

func TestResolveThinNixhome_DevBuildCoercesToDefaultRef(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		Version:  "v0.0.0",
		StatFunc: func(string) error { return errNotFound{} },
	})
	if !got.Remote {
		t.Errorf("dev build clean machine: want remote, got local: %+v", got)
	}
	want := "github:DimmKirr/devcell/" + runner.DefaultNixhomeGitRef + "?dir=nixhome"
	if got.FlakeRef != want {
		t.Errorf("FlakeRef (v0.0.0 coerced): want %q, got %q", want, got.FlakeRef)
	}
}

type errNotFound struct{}

func (errNotFound) Error() string { return "stat: no such file" }
