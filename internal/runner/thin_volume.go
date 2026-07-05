package runner

import (
	"os"
	"strings"
)

// DefaultThinStoreVolume is the named Docker volume that holds the thin-mode
// /nix store across builds and cell runs. Single shared volume by default so
// stack rebuilds reuse the existing nix store.
const DefaultThinStoreVolume = "devcell-nix-store"

// ThinStoreVolume returns the volume name to use for the thin /nix store.
// Reads DEVCELL_NIX_VOLUME (trimmed); falls back to DefaultThinStoreVolume.
// Override is per-process — tests use it for parallel/isolated runs with
// cleanup; users may use it for side-by-side installations.
func ThinStoreVolume() string {
	if v := strings.TrimSpace(os.Getenv("DEVCELL_NIX_VOLUME")); v != "" {
		return v
	}
	return DefaultThinStoreVolume
}
