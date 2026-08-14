package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/DimmKirr/devcell/internal/wimlib"
)

// WimRegistryPatch describes a set of DWORD modifications to apply to a
// registry hive inside a WIM image. The hive is extracted to a temp file,
// patched, and written back.
type WimRegistryPatch struct {
	// HivePath is the path inside the WIM image
	// (e.g. `\Windows\System32\config\SYSTEM`).
	HivePath string
	// Patches are the DWORD values to overwrite.
	Patches []goregedit.DWordPatch
}

// PatchWimRegistry extracts a registry hive from a WIM image, applies
// DWORD patches, and writes the modified hive back. The WIM is NOT
// overwritten — call wim.Overwrite() after all modifications are done.
// The returned cleanup function removes the temp directory holding the
// patched hive; call it AFTER wim.Overwrite() completes since wimlib
// needs the file to exist at overwrite time.
func PatchWimRegistry(wim *wimlib.WIM, imageNum int, rp WimRegistryPatch) (cleanup func(), err error) {
	noop := func() {}
	if len(rp.Patches) == 0 {
		return noop, nil
	}

	tmpDir, err := os.MkdirTemp("", "goregedit-*")
	if err != nil {
		return noop, fmt.Errorf("creating temp dir: %w", err)
	}

	rm := func() { os.RemoveAll(tmpDir) }

	if err := wim.ExtractPaths(imageNum, tmpDir, []string{rp.HivePath}); err != nil {
		rm()
		return noop, fmt.Errorf("extracting %s: %w", rp.HivePath, err)
	}

	localPath := filepath.Join(tmpDir, filepath.FromSlash(strings.ReplaceAll(rp.HivePath, `\`, `/`)))
	if err := goregedit.ApplyDWordPatches(localPath, rp.Patches); err != nil {
		rm()
		return noop, fmt.Errorf("patching %s: %w", rp.HivePath, err)
	}

	if err := wim.UpdateImageAdd(imageNum, localPath, rp.HivePath); err != nil {
		rm()
		return noop, fmt.Errorf("writing back %s: %w", rp.HivePath, err)
	}

	return rm, nil
}

// HyperVBootPatches returns the registry patches needed to make Hyper-V
// services start at boot in WinPE. Without these, hvservice has Start=3
// (Manual) and never loads.
func HyperVBootPatches() WimRegistryPatch {
	return WimRegistryPatch{
		HivePath: `\Windows\System32\config\SYSTEM`,
		Patches: []goregedit.DWordPatch{
			// Kernel drivers — must load at boot (Start=0).
			{KeyPath: `ControlSet001\Services\hvservice`, ValueName: "Start", Value: 0},
			{KeyPath: `ControlSet001\Services\vmbusr`, ValueName: "Start", Value: 0},

			// vmbus has Start=0 but StartOverride "0"=3 downgrades it to Manual.
			{KeyPath: `ControlSet001\Services\vmbus\StartOverride`, ValueName: "0", Value: 0},

			// Win32 services — Auto (2) so SCM starts them in WinPE.
			{KeyPath: `ControlSet001\Services\HvHost`, ValueName: "Start", Value: 2},
			{KeyPath: `ControlSet001\Services\vmcompute`, ValueName: "Start", Value: 2},
		},
	}
}
