package qemu

import (
	"fmt"
	"os"
	"os/exec"
)

// FinalizeSpec derives the dev-env boot from a build spec: the same guest
// (disk, network identity, credentials) on the EL3 machine — secure=on with
// a kernel-loaded relocatable firmware — which is what lets Windows' own
// hypervisor, and therefore WSL2, run (docs/spec/QEMU-ARM64-WINDOWS11-WSL2-NIX.md §2.2).
//
// The install boot and this one are intentionally different machines: the
// installer is proven on the plain pflash machine, the WSL2 stack on this
// one. Only the boot environment changes; everything identifying the guest
// is carried over.
func FinalizeSpec(build Spec, kernelFirmware string) Spec {
	fin := build
	fin.VMName = build.VMName + "-devenv"
	fin.SecureWorld = true
	fin.FirmwareKernel = true
	fin.FirmwarePath = kernelFirmware
	// -kernel loading has no pflash NVRAM bank, and the secure machine
	// supersedes the NestedVirt one.
	fin.VarsPath = ""
	fin.NestedVirt = false
	// Install media stays behind.
	fin.VirtioISO = ""
	return fin
}

// VirtiofsdPath resolves the host-side virtio-fs daemon:
// $DEVCELL_VIRTIOFSD, then PATH.
func VirtiofsdPath() (string, error) {
	if p := os.Getenv("DEVCELL_VIRTIOFSD"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("virtiofsd: %w", err)
		}
		return p, nil
	}
	if p, err := exec.LookPath("virtiofsd"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf(
		"virtiofsd not found: set DEVCELL_VIRTIOFSD or put it on PATH (nix build nixpkgs#virtiofsd)")
}

// VirtiofsdCommand builds the daemon invocation for a project share.
// --sandbox none: the default sandbox needs user namespaces the devcell
// container does not have. The caller owns the process — virtiofsd exits
// whenever its client disconnects, so it must be started fresh for every VM
// boot that mounts the share.
func VirtiofsdCommand(bin, socketPath, sharedDir string) *exec.Cmd {
	return exec.Command(bin,
		"--socket-path", socketPath,
		"--shared-dir", sharedDir,
		"--sandbox", "none")
}
