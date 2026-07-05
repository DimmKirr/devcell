package runner

import (
	"sync"

	"github.com/DimmKirr/devcell/internal/ux"
)

var thickDeprecationOnce sync.Once

// WarnThickDeprecation emits a one-time deprecation warning when the user
// opts out of thin mode (`--no-thin`, `--thick`, `thin = false` in TOML, or
// `DEVCELL_THIN=0`). Thin mode (Docker volume nix store) is the canonical
// path post-Modules-2.0; non-thin / "thick" image builds are kept for
// backwards compatibility and will be removed in a future release.
//
// Safe to call from multiple sites (cmd/root.go, cmd/build.go); the message
// fires at most once per process via sync.Once.
func WarnThickDeprecation() {
	thickDeprecationOnce.Do(func() {
		ux.Warn("Non-thin (thick) image mode is deprecated and will be removed. " +
			"Drop `--no-thin` / `--thick` / `thin = false` to use the canonical thin path.")
	})
}
