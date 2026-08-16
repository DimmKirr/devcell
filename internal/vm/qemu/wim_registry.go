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

// WimDWordCheck describes a single registry DWORD expectation for
// VerifyWimRegistry.
type WimDWordCheck struct {
	KeyPath   string
	ValueName string
	Expected  uint32
	Optional  bool
}

// VerifyWimRegistry extracts a registry hive from a WIM image and verifies
// that every check's DWORD value matches the expectation. Returns the first
// non-optional mismatch as an error. Optional checks that fail (key/value
// missing) are silently skipped.
func VerifyWimRegistry(wim *wimlib.WIM, imageNum int, hivePath string, checks []WimDWordCheck) error {
	tmpDir, err := os.MkdirTemp("", "goregedit-verify-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := wim.ExtractPaths(imageNum, tmpDir, []string{hivePath}); err != nil {
		return fmt.Errorf("extracting %s: %w", hivePath, err)
	}

	localPath := filepath.Join(tmpDir, filepath.FromSlash(strings.ReplaceAll(hivePath, `\`, `/`)))
	for _, c := range checks {
		val, err := goregedit.ReadDWord(localPath, c.KeyPath, c.ValueName)
		if err != nil {
			if c.Optional {
				continue
			}
			return fmt.Errorf("reading %s\\%s: %w", c.KeyPath, c.ValueName, err)
		}
		if val != c.Expected {
			return fmt.Errorf("%s\\%s: got %d, want %d", c.KeyPath, c.ValueName, val, c.Expected)
		}
	}
	return nil
}

// HyperVBootChecks returns the registry value expectations corresponding
// to HyperVBootPatches. Use with VerifyWimRegistry to assert patches
// were applied correctly.
func HyperVBootChecks() []WimDWordCheck {
	return []WimDWordCheck{
		{KeyPath: `ControlSet001\Services\hvservice`, ValueName: "Start", Expected: 0, Optional: true},
		{KeyPath: `ControlSet001\Services\vmbusr`, ValueName: "Start", Expected: 0, Optional: true},
		{KeyPath: `ControlSet001\Services\vmbus\StartOverride`, ValueName: "0", Expected: 0, Optional: true},
		{KeyPath: `ControlSet001\Services\HvHost`, ValueName: "Start", Expected: 2, Optional: true},
		{KeyPath: `ControlSet001\Services\vmcompute`, ValueName: "Start", Expected: 2, Optional: true},
	}
}

// HyperVBootPatches returns the registry patches needed to make Hyper-V
// services start at boot in WinPE. Without these, hvservice has Start=3
// (Manual) and never loads.
func HyperVBootPatches() WimRegistryPatch {
	return WimRegistryPatch{
		HivePath: `\Windows\System32\config\SYSTEM`,
		Patches: []goregedit.DWordPatch{
			// Kernel drivers — must load at boot (Start=0).
			// Optional: not every boot.wim ships every service key.
			{KeyPath: `ControlSet001\Services\hvservice`, ValueName: "Start", Value: 0, Optional: true},
			{KeyPath: `ControlSet001\Services\vmbusr`, ValueName: "Start", Value: 0, Optional: true},

			// vmbus has Start=0 but StartOverride "0"=3 downgrades it to Manual.
			{KeyPath: `ControlSet001\Services\vmbus\StartOverride`, ValueName: "0", Value: 0, Optional: true},

			// Win32 services — Auto (2) so SCM starts them in WinPE.
			{KeyPath: `ControlSet001\Services\HvHost`, ValueName: "Start", Value: 2, Optional: true},
			{KeyPath: `ControlSet001\Services\vmcompute`, ValueName: "Start", Value: 2, Optional: true},
		},
	}
}
