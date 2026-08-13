//go:build !wimlib

package qemu

import "fmt"

func InjectWinPEPayload(bootWimPath, injectDir string, registryPatches ...WimRegistryPatch) error {
	return fmt.Errorf("wimlib not available — build with -tags wimlib")
}
