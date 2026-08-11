package runner

import (
	"strings"
)

// CELL-418: detect whether the thin image's baked-in nix closure still
// exists on the shared nix-store volume. A closure dies when a later
// build for a different stack triggers GC that reaps the store paths
// the image's profile symlink points at.

// ProfilePath is the canonical location of the home-manager profile
// baked into every thin image by thin_build.go.
const ProfilePath = "/opt/devcell/.local/state/nix/profiles/profile"

// ClosureAliveArgv returns the docker argv for the closure-alive probe.
// Runs inside the target image with the nix volume mounted so the
// profile's /nix/store/... symlink target can be resolved.
func ClosureAliveArgv(volume, image string) []string {
	script := `t=$(readlink -f "` + ProfilePath + `" 2>/dev/null) && [ -n "$t" ] && [ -d "$t" ] && printf '%s\n' "$t"`
	return []string{
		"docker", "run", "--rm", "--network", "none",
		"-v", volume + ":/nix",
		"--entrypoint", "/bin/sh",
		image, "-c", script,
	}
}

// ClosureDeadWarning returns a user-facing warning and dead=true when the
// closure probe reports the image's nix paths are gone.
func ClosureDeadWarning(alive bool) (warning string, dead bool) {
	if alive {
		return "", false
	}
	return "This image's nix closure was garbage collected. Rebuild? (Y/n)", true
}

// ParseClosureAliveResult interprets the probe's stdout + error.
// Returns the resolved store path and whether the closure is alive.
func ParseClosureAliveResult(stdout string, err error) (resolvedPath string, alive bool) {
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(stdout)
	if p == "" {
		return "", false
	}
	return p, true
}
