package libvirt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// PathMapping rewrites one container path prefix to its host equivalent.
type PathMapping struct {
	From string // container-side prefix (bind mount target)
	To   string // host-side prefix (bind mount source)
}

// PathMap translates container paths to host paths for domain XML.
//
// Empty means the CLI already runs on the host — every path passes through.
// Non-empty means strict translation: QEMU on the host cannot open a
// container-only path, so an unmapped path is an error, not a passthrough.
type PathMap []PathMapping

// TranslateToHost rewrites p using the longest matching mapping prefix.
// Prefixes match on path boundaries only: /devcell-1555 does not match a
// /devcell-155 mapping.
func (m PathMap) TranslateToHost(p string) (string, error) {
	if len(m) == 0 {
		return p, nil
	}
	clean := filepath.Clean(p)

	best := -1
	bestLen := -1
	for i, mp := range m {
		from := filepath.Clean(mp.From)
		if clean != from && !strings.HasPrefix(clean, from+"/") {
			continue
		}
		if len(from) > bestLen {
			best, bestLen = i, len(from)
		}
	}
	if best < 0 {
		return "", fmt.Errorf("path %q is outside every libvirt path mapping — QEMU on the host cannot open it (add a [cell] libvirt_path_map entry)", p)
	}

	from := filepath.Clean(m[best].From)
	to := filepath.Clean(m[best].To)
	if clean == from {
		return to, nil
	}
	return to + strings.TrimPrefix(clean, from), nil
}

// TranslateSpecPaths returns a copy of spec with every field QEMU opens on
// the host rewritten through the map. Empty fields stay empty; the input is
// not mutated. SSHKeyPath is deliberately absent: the ssh client runs on the
// CLI side, in the container namespace.
func TranslateSpecPaths(spec qemu.Spec, m PathMap) (qemu.Spec, error) {
	out := spec
	for _, f := range []struct {
		name string
		p    *string
	}{
		{"DiskPath", &out.DiskPath},
		{"FirmwarePath", &out.FirmwarePath},
		{"VarsPath", &out.VarsPath},
		{"VirtioISO", &out.VirtioISO},
		{"SerialLogPath", &out.SerialLogPath},
		{"GuestProgressLogPath", &out.GuestProgressLogPath},
	} {
		if *f.p == "" {
			continue
		}
		t, err := m.TranslateToHost(*f.p)
		if err != nil {
			return qemu.Spec{}, fmt.Errorf("%s: %w", f.name, err)
		}
		*f.p = t
	}
	return out, nil
}
