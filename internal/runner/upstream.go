package runner

import "fmt"

// Canonical upstream nixhome source — single source of truth across the CLI.
// Previously these constants were re-encoded in 4 separate fmt.Sprintf calls
// (pure_nixhome_resolver, cmd/modules, scaffold templates). Centralised here
// so a fork/rename is a one-line change.
const (
	UpstreamOwner  = "DimmKirr"
	UpstreamRepo   = "devcell"
	UpstreamSubdir = "nixhome"
)

// UpstreamFlakeRef returns the canonical github flake reference for the
// devcell nixhome, pinned to `ref`. Empty / "v0.0.0" (dev build) coerces to
// DefaultNixhomeGitRef so dev builds always point at a real branch.
//
// Example: UpstreamFlakeRef("v1.0.0") → "github:DimmKirr/devcell/v1.0.0?dir=nixhome"
func UpstreamFlakeRef(ref string) string {
	if ref == "" || ref == "v0.0.0" {
		ref = DefaultNixhomeGitRef
	}
	return fmt.Sprintf("github:%s/%s/%s?dir=%s", UpstreamOwner, UpstreamRepo, ref, UpstreamSubdir)
}

// UpstreamFlakeRefNoVersion returns the unpinned ref — used by introspection
// commands (`cell modules list`) that want the catalog as it exists upstream
// right now, not pinned to the CLI binary's compile-time version.
func UpstreamFlakeRefNoVersion() string {
	return fmt.Sprintf("github:%s/%s?dir=%s", UpstreamOwner, UpstreamRepo, UpstreamSubdir)
}
