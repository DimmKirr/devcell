package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// ResolvePureNixhomeRef mirrors the docker path's nixhome resolution chain
// (internal/scaffold/scaffold.go:130-140) but emits a flake reference instead
// of editing a generated flake.nix. Tested as a pure function — fs lookups
// injected.
//
// Precedence:
//   1. tomlNixhome (from [cell].nixhome or DEVCELL_NIXHOME_PATH env)
//   2. <baseDir>/nixhome on disk
//   3. github:DimmKirr/devcell/<ver>?dir=nixhome (with v0.0.0 → main coercion)

func TestResolvePureNixhomeRef_TomlNixhomeWins(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		TomlNixhome: "/explicit/path",
		BaseDir:     "/project",
		Version:     "v1.2.3",
		StatFunc:    func(string) error { return nil }, // anything exists
	})
	if got.FlakeRef != "path:/explicit/path" {
		t.Errorf("toml nixhome → want path:/explicit/path, got %q", got.FlakeRef)
	}
	if got.LocalPath != "/explicit/path" {
		t.Errorf("LocalPath = %q, want /explicit/path", got.LocalPath)
	}
	if got.Remote {
		t.Errorf("Remote = true; want false for local toml path")
	}
}

func TestResolvePureNixhomeRef_FallsBackToProjectNixhome(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir: "/project",
		Version: "v1.2.3",
		StatFunc: func(p string) error {
			if p == "/project/nixhome" {
				return nil
			}
			return os.ErrNotExist
		},
	})
	if got.FlakeRef != "path:/project/nixhome" {
		t.Errorf("local fallback → want path:/project/nixhome, got %q", got.FlakeRef)
	}
	if got.LocalPath != "/project/nixhome" {
		t.Errorf("LocalPath = %q, want /project/nixhome", got.LocalPath)
	}
	if got.Remote {
		t.Errorf("Remote = true; want false for local fallback")
	}
}

func TestResolvePureNixhomeRef_NoLocal_UsesGithubFallback(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir:  "/project",
		Version:  "v1.2.3",
		StatFunc: func(string) error { return os.ErrNotExist },
	})
	want := "github:DimmKirr/devcell/v1.2.3?dir=nixhome"
	if got.FlakeRef != want {
		t.Errorf("github fallback → want %q, got %q", want, got.FlakeRef)
	}
	if got.LocalPath != "" {
		t.Errorf("LocalPath = %q; want empty for remote ref", got.LocalPath)
	}
	if !got.Remote {
		t.Errorf("Remote = false; want true for github fallback")
	}
}

func TestResolvePureNixhomeRef_V000CoercesToDefaultRef(t *testing.T) {
	// Dev builds and untagged releases ship with version.Version="v0.0.0".
	// Both scaffold and pure paths coerce that to runner.DefaultNixhomeGitRef
	// so unreleased clients still fetch a working branch (feature/wip while
	// the pure path lives off main).
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir:  "/project",
		Version:  "v0.0.0",
		StatFunc: func(string) error { return os.ErrNotExist },
	})
	want := "github:DimmKirr/devcell/" + runner.DefaultNixhomeGitRef + "?dir=nixhome"
	if got.FlakeRef != want {
		t.Errorf("v0.0.0 → want %q, got %q", want, got.FlakeRef)
	}
}

func TestResolvePureNixhomeRef_EmptyVersionCoercesToDefaultRef(t *testing.T) {
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir:  "/project",
		Version:  "",
		StatFunc: func(string) error { return os.ErrNotExist },
	})
	want := "github:DimmKirr/devcell/" + runner.DefaultNixhomeGitRef + "?dir=nixhome"
	if got.FlakeRef != want {
		t.Errorf("empty version → want %q, got %q", want, got.FlakeRef)
	}
}

// Pins the default ref value so an accidental rename of the constant gets
// caught by CI. Update this when the pure path lands on main.
func TestDefaultNixhomeGitRef_IsFeatureWip(t *testing.T) {
	if runner.DefaultNixhomeGitRef != "feature/wip" {
		t.Errorf("DefaultNixhomeGitRef = %q; want \"feature/wip\" (flip to \"main\" when CELL-195 lands)",
			runner.DefaultNixhomeGitRef)
	}
}

// Integration smoke: real filesystem with the project's own nixhome/.
// Validates StatFunc default (os.Stat) and absolute-path resolution.
func TestResolvePureNixhomeRef_RealFilesystem(t *testing.T) {
	tmp := t.TempDir()
	nixhomeDir := filepath.Join(tmp, "nixhome")
	if err := os.Mkdir(nixhomeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := runner.ResolvePureNixhomeRef(runner.PureNixhomeInputs{
		BaseDir: tmp,
		Version: "v1.0.0",
	})
	want := "path:" + nixhomeDir
	if got.FlakeRef != want {
		t.Errorf("real fs local nixhome → want %q, got %q", want, got.FlakeRef)
	}
}
