//go:build wimlib

package qemu

import (
	"fmt"
	"path/filepath"

	"github.com/DimmKirr/devcell/internal/wimlib"
)

// InjectWinPEPayload uses wimlib to inject WinPE agent files into boot.wim
// image 2. The injectDir must contain winpeshl.ini, bootstrap.cmd, agent.cmd,
// and optionally vioserial drivers under drivers/. The WIM is modified in-place.
func InjectWinPEPayload(bootWimPath, injectDir string, registryPatches ...WimRegistryPatch) error {
	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available — build with -tags wimlib")
	}

	wim, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fmt.Errorf("opening boot.wim: %w", err)
	}
	defer wim.Close()

	count, err := wim.ImageCount()
	if err != nil {
		return fmt.Errorf("getting image count: %w", err)
	}
	if count < 2 {
		return fmt.Errorf("boot.wim has %d images, need at least 2", count)
	}

	const imageNum = 2

	if err := wim.UpdateImageAdd(imageNum,
		filepath.Join(injectDir, "winpeshl.ini"),
		`\Windows\System32\winpeshl.ini`); err != nil {
		return fmt.Errorf("adding winpeshl.ini: %w", err)
	}

	if err := wim.UpdateImageAddTree(imageNum, injectDir, `\devcell`); err != nil {
		return fmt.Errorf("adding devcell tree: %w", err)
	}

	for _, rp := range registryPatches {
		cleanup, err := PatchWimRegistry(wim, imageNum, rp)
		if err != nil {
			return fmt.Errorf("patching registry %s: %w", rp.HivePath, err)
		}
		defer cleanup()
	}

	if err := wim.Overwrite(); err != nil {
		return fmt.Errorf("overwriting boot.wim: %w", err)
	}

	return nil
}
