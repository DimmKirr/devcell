package runner

import (
	"os"
	"path/filepath"
)

// DefaultNixhomeGitRef is the github branch/tag used when no local nixhome is
// available and the cell binary doesn't carry a release version (v0.0.0 / dev
// builds). Set to feature/wip while CELL-195 lives off main; flip back to
// "main" when the pure path lands.
const DefaultNixhomeGitRef = "feature/wip"

// PureNixhomeInputs is the input to ResolvePureNixhomeRef. Mirrors the data
// flow already present for the docker path (scaffold.go:130-140), but emits a
// flake reference instead of editing a generated flake.nix.
type PureNixhomeInputs struct {
	// TomlNixhome is the resolved [cell].nixhome value (project TOML →
	// global TOML → DEVCELL_NIXHOME_PATH env, merged by cfg.LoadFromOS).
	// Empty if unset.
	TomlNixhome string

	// BaseDir is the project working directory — used to check for a local
	// "<BaseDir>/nixhome" as the second-level fallback.
	BaseDir string

	// Version is the cell binary version (version.Version). Used to pin the
	// remote github ref. "v0.0.0" and "" are coerced to DefaultNixhomeGitRef
	// (dev builds).
	Version string

	// StatFunc lets tests inject a synthetic filesystem. When nil, real
	// os.Stat is used.
	StatFunc func(path string) error
}

// PureNixhomeRef is the resolved flake reference plus metadata the caller
// needs to decide whether to SyncNixhome into BuildDir.
type PureNixhomeRef struct {
	// FlakeRef is the value to pass through to PureBuildSpec.FlakeRef.
	// Format: "path:<abs>" for local sources, "github:..." for remote.
	FlakeRef string

	// LocalPath is the absolute on-disk path when FlakeRef is "path:" form;
	// empty when remote. Caller uses this to decide whether to sync into
	// BuildDir (only local sources need staging).
	LocalPath string

	// Remote is true when FlakeRef is a network URL (github:, git+https:, …).
	// Convenience flag — equivalent to LocalPath == "".
	Remote bool
}

// ResolvePureNixhomeRef applies the docker path's nixhome resolution chain to
// produce a flake reference for the pure build.
//
// Precedence:
//  1. inputs.TomlNixhome (explicit user setting via .devcell.toml / env)
//  2. inputs.BaseDir + "/nixhome" on disk
//  3. github:DimmKirr/devcell/<Version>?dir=nixhome (Version coerced to
//     DefaultNixhomeGitRef when empty or "v0.0.0")
//
// Pure function — fs lookups go through inputs.StatFunc so tests don't
// depend on real disk state.
func ResolvePureNixhomeRef(inputs PureNixhomeInputs) PureNixhomeRef {
	stat := inputs.StatFunc
	if stat == nil {
		stat = func(p string) error { _, err := os.Stat(p); return err }
	}

	// 1. Explicit TOML/env wins. Don't stat — user said "use this path",
	//    surface the failure at build time rather than silently falling back.
	if inputs.TomlNixhome != "" {
		return PureNixhomeRef{
			FlakeRef:  "path:" + inputs.TomlNixhome,
			LocalPath: inputs.TomlNixhome,
		}
	}

	// 2. Local <BaseDir>/nixhome — same fallback the runBuildPure had before.
	if inputs.BaseDir != "" {
		local := filepath.Join(inputs.BaseDir, "nixhome")
		if stat(local) == nil {
			return PureNixhomeRef{
				FlakeRef:  "path:" + local,
				LocalPath: local,
			}
		}
	}

	// 3. Remote github fallback — delegates to the single source of truth
	//    in upstream.go (v0.0.0 → DefaultNixhomeGitRef coercion).
	return PureNixhomeRef{
		FlakeRef: UpstreamFlakeRef(inputs.Version),
		Remote:   true,
	}
}
