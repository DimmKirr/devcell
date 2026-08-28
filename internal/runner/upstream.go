package runner

import (
	"fmt"
	"os"
	"strings"
)

// Canonical upstream nixhome source — single source of truth across the CLI.
// Previously these constants were re-encoded in 4 separate fmt.Sprintf calls
// (pure_nixhome_resolver, cmd/modules, scaffold templates). Centralised here
// so a fork/rename is a one-line change.
const (
	UpstreamOwner = "devcell-sh"
	UpstreamRepo  = "community-home"
	// UpstreamSubdir is empty since the nixhome moved to its own repo
	// (community-home); the flake now lives at the repo root.
	UpstreamSubdir = ""
)

// UpstreamFlakeRef returns the canonical github flake reference for the
// devcell nixhome, pinned to `ref`. Empty / "v0.0.0" / dev-version coerces
// to DefaultNixhomeGitRef so dev builds always point at a real branch.
//
// Example: UpstreamFlakeRef("v1.0.0") → "github:devcell-sh/community-home/v1.0.0"
func UpstreamFlakeRef(ref string) string {
	if ref == "" || ref == "v0.0.0" || isDevVersion(ref) {
		ref = DefaultNixhomeGitRef
	}
	s := fmt.Sprintf("github:%s/%s/%s", UpstreamOwner, UpstreamRepo, ref)
	if UpstreamSubdir != "" {
		s += "?dir=" + UpstreamSubdir
	}
	return s
}

// ResolveNixhomeRef returns the nixhome source to use for builds.
// Precedence: DEVCELL_NIXHOME > DEVCELL_NIXHOME_PATH (legacy) > default upstream flake ref.
// Accepts local paths, github: flake refs, or https:// git URLs.
func ResolveNixhomeRef(ver string) string {
	if v := os.Getenv("DEVCELL_NIXHOME"); v != "" {
		return v
	}
	if v := os.Getenv("DEVCELL_NIXHOME_PATH"); v != "" {
		return v
	}
	return UpstreamFlakeRef(ver)
}

// isDevVersion returns true for git-describe versions that don't correspond
// to a real remote tag/branch (e.g. "v0.8.2-94-g0ac6be1-dirty").
func isDevVersion(v string) bool {
	return strings.Contains(v, "-g") || strings.Contains(v, "-dirty")
}

// UpstreamFlakeRefNoVersion returns the unpinned ref — used by introspection
// commands (`cell modules list`) that want the catalog as it exists upstream
// right now, not pinned to the CLI binary's compile-time version.
func UpstreamFlakeRefNoVersion() string {
	s := fmt.Sprintf("github:%s/%s", UpstreamOwner, UpstreamRepo)
	if UpstreamSubdir != "" {
		s += "?dir=" + UpstreamSubdir
	}
	return s
}
