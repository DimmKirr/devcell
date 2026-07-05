package runner

import (
	"context"
	"os/exec"
	"strings"
)

// ThinEntrypointSentinel is the path the thin image's ENTRYPOINT references
// (built by thin_build.go:259). Used as the hydration probe in cmd/root.go's
// auto-build gate (CELL-38).
const ThinEntrypointSentinel = "/nix/var/nix/profiles/devcell-tools/bin/tini"

// VolumeHydrated reports whether a named Docker volume exists AND contains the
// given sentinel path. Use to gate auto-build in `cell shell`/`claude`/... so
// a stale or pruned `/nix` volume triggers a rebuild instead of failing later
// at the kernel's `exec: tini: no such file or directory` (CELL-40/332).
//
// Pure function — caller injects the two probes so unit tests don't need
// docker. For runtime use, see VolumeExists and VolumeContains below.
func VolumeHydrated(volumeName, sentinelPath string, exists func(string) bool, probe func(string, string) bool) bool {
	if !exists(volumeName) {
		return false
	}
	return probe(volumeName, sentinelPath)
}

// VolumeExists shells out to `docker volume inspect` — returns true on exit 0.
func VolumeExists(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "docker", "volume", "inspect", name).Run() == nil
}

// VolumeContainsArgv returns the docker argv used by VolumeContains. Pure so
// the mount layout can be unit-tested without spawning docker.
//
// The volume MUST be mounted at /nix (not /probe). Nix profile entries are
// absolute symlinks rooted at /nix/store/...; mounting elsewhere makes
// `test -e` follow a dangling target and report MISSING for a perfectly
// hydrated volume (CELL-45). Mounting at /nix matches what the builder and
// runner already do, so absolute symlinks resolve in-container.
func VolumeContainsArgv(volumeName, sentinelPath string) []string {
	return []string{
		"docker", "run", "--rm",
		"-v", volumeName + ":/nix:ro",
		"busybox:1.36",
		"test", "-e", sentinelPath,
	}
}

// VolumeContains spawns a throwaway busybox container (~5 MB) to test whether
// a sentinel path exists inside the named volume. sentinelPath must be the
// absolute in-container path the image's ENTRYPOINT references (e.g.
// /nix/var/nix/profiles/devcell-tools/bin/tini); see VolumeContainsArgv for
// the mount-layout rationale.
func VolumeContains(ctx context.Context, volumeName, sentinelPath string) bool {
	if !strings.HasPrefix(sentinelPath, "/nix/") && sentinelPath != "/nix" {
		return false
	}
	argv := VolumeContainsArgv(volumeName, sentinelPath)
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run() == nil
}
