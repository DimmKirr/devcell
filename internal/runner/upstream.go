package runner

import "fmt"

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
// devcell nixhome, pinned to `ref`. Empty / "v0.0.0" (dev build) coerces to
// DefaultNixhomeGitRef so dev builds always point at a real branch.
//
// Example: UpstreamFlakeRef("v1.0.0") → "github:devcell-sh/community-home/v1.0.0"
func UpstreamFlakeRef(ref string) string {
	if ref == "" || ref == "v0.0.0" {
		ref = DefaultNixhomeGitRef
	}
	s := fmt.Sprintf("github:%s/%s/%s", UpstreamOwner, UpstreamRepo, ref)
	if UpstreamSubdir != "" {
		s += "?dir=" + UpstreamSubdir
	}
	return s
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
