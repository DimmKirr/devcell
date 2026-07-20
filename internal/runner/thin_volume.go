package runner

import (
	"os"
	"runtime"
	"strings"
)

// DefaultThinStoreVolume is the named Docker volume that holds the thin-mode
// /nix store across builds and cell runs. Single shared volume by default so
// stack rebuilds reuse the existing nix store.
const DefaultThinStoreVolume = "devcell-nix-store"

// ThinStoreVolume returns the volume name to use for the thin /nix store.
// Reads DEVCELL_NIX_VOLUME (trimmed); falls back to DefaultThinStoreVolume.
// When DEVCELL_ARCH requests a non-native architecture (e.g. amd64 on an ARM
// host), the default volume name gets an arch suffix so cross-arch builds use
// a separate nix store — mixing architectures on a single volume wastes space
// and causes dangling-symlink issues on every build.
func ThinStoreVolume() string {
	if v := strings.TrimSpace(os.Getenv("DEVCELL_NIX_VOLUME")); v != "" {
		return v
	}
	if suffix := crossArchSuffix(); suffix != "" {
		return DefaultThinStoreVolume + "-" + suffix
	}
	return DefaultThinStoreVolume
}

// crossArchSuffix returns the DEVCELL_ARCH value (normalised to Docker's
// convention: "amd64" or "arm64") when it differs from the host architecture.
// Returns "" when DEVCELL_ARCH is unset or matches the host.
func crossArchSuffix() string {
	v := strings.TrimSpace(os.Getenv("DEVCELL_ARCH"))
	if v == "" {
		return ""
	}
	docker := dockerArch(v)
	if docker == "" {
		return ""
	}
	if docker == nativeDockerArch() {
		return ""
	}
	return docker
}

func dockerArch(s string) string {
	switch s {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return ""
}

func nativeDockerArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}
