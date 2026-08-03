package qemu

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// guestFS is the guest-side PowerShell tree: a shared module plus one script
// per stage. Unlike templates/, nothing here is rendered — it is real
// PowerShell, embedded verbatim and delivered on the per-run control volume.
//
// That distinction is the point of CELL-402: Go-interpolated PowerShell is
// never linted, never runnable standalone, and fails only on a live guest
// minutes-to-hours in (lost quotes killed a 40-minute pipeline; an
// interpolated colon broke icacls). Real files remove the bug class.
//
//go:embed guest
var guestFS embed.FS

// GuestControlDir is where the guest tree lands on the control volume.
const GuestControlDir = "/devcell"

// GuestFile returns one file from the embedded guest tree, addressed the way
// stages refer to it ("Devcell.psm1", "stages/wsl2-enable.ps1").
func GuestFile(name string) ([]byte, error) {
	data, err := guestFS.ReadFile(path.Join("guest", name))
	if err != nil {
		return nil, fmt.Errorf("guest file %s: %w", name, err)
	}
	return data, nil
}

// GuestPayload returns the whole guest tree keyed by its path on the control
// volume, ready for BuildControlVolume. Everything ships every run: the
// volume is built on the host and attached at boot, so it can never drift
// from the repo the way a copy written into the qcow2 would.
func GuestPayload() (map[string][]byte, error) {
	payload := map[string][]byte{}
	err := fs.WalkDir(guestFS, "guest", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := guestFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		payload[GuestControlDir+"/"+strings.TrimPrefix(p, "guest/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collecting guest payload: %w", err)
	}
	return payload, nil
}
