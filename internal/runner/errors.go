package runner

import "strings"

// TranslateError converts a raw subprocess error from nix/docker/home-manager
// into a user-friendly one-liner with a next-action hint. Unknown errors pass
// through verbatim (no false-positive translation).
//
// Used by CLI command handlers when wrapping `nix eval`, `docker run`, and
// `home-manager switch` failures. The agent's first 10 minutes should never
// dump a raw nix/docker traceback at the user.
func TranslateError(err error) string {
	if err == nil {
		return ""
	}
	raw := err.Error()
	lower := strings.ToLower(raw)

	switch {
	// Docker daemon not running — first-time install of Docker Desktop or OrbStack
	// where the daemon hasn't been started yet.
	case strings.Contains(lower, "cannot connect to the docker daemon") ||
		strings.Contains(lower, "docker daemon"):
		return "Docker isn't running. Start Docker Desktop / OrbStack / Colima and re-run."

	// Network glitch fetching a Nix-pinned source (registry hash != download hash).
	// Usually transient; a retry fixes it.
	case strings.Contains(lower, "hash mismatch in fixed-output derivation"):
		return "Nix dep hash mismatch — usually a transient network/registry issue. Re-run; if it persists, refresh your flake pin with `devcell build --update`."

	// Out of disk space — common during ultimate stack first build.
	case strings.Contains(lower, "no space left on device"):
		return "Out of disk space. Free some up with `docker system prune -a` or `devcell build prune`, then re-run."

	// Image not in registry — usually means the user picked a custom
	// stack+modules combo no pre-built image covers, or a tag was rotated out.
	case strings.Contains(lower, "pull access denied") ||
		strings.Contains(lower, "repository does not exist") ||
		strings.Contains(lower, "manifest unknown"):
		return "Pre-built image not found for this stack+modules combo. Run `devcell build` to rebuild locally (~5–10 min)."

	// Generic nix build failure — directs the user to the build log.
	case strings.Contains(lower, "builder for") && strings.Contains(lower, "failed with exit code"):
		return "Nix build failed. Check `.devcell/build.log` for details. Most common cause: a custom module has a typo or a fetched source moved."

	// Missing nix attribute — usually a typo in module name or a wrong flake ref.
	case strings.Contains(lower, "attribute") && strings.Contains(lower, "missing"):
		return "Nix attribute missing (likely a typo or wrong flake ref): `" + raw + "`. Run `devcell modules list` to see valid module names."

	// Authentication failure — Claude Max session expired, etc.
	case strings.Contains(lower, "authentication") || strings.Contains(lower, "401 unauthorized"):
		return "Authentication failed. Re-run with `cell login <provider>` to refresh the session."

	// Host UID mismatch — files in mounted project become owned by wrong UID.
	case strings.Contains(lower, "uid") && strings.Contains(lower, "mismatch"):
		return "UID mismatch between host and container. Pass `--uid $(id -u)` or check `[cell].docker_privileged` in .devcell.toml."

	// Port already in use — common when multiple cells try to claim the same port.
	case strings.Contains(lower, "address already in use") ||
		strings.Contains(lower, "bind: address already"):
		return "Port already in use. Another cell or a host process owns the port. Stop it, or set `[ports].forward` in .devcell.toml to remap."

	// Mise install failure — fallback before the install version reaches lockfile.
	case strings.Contains(lower, "mise") && strings.Contains(lower, "install failed"):
		return "Mise install failed. Check `.tool-versions` for an invalid version. Re-run with `mise install -y` once inside the cell."

	default:
		// Unknown error — pass through unchanged so power users still see
		// the raw output and `--debug` callers get the full trace.
		return raw
	}
}
