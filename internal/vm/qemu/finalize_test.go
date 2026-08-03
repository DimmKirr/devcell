package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The finalization phase boots the just-installed template on the EL3
// machine. The derived spec must keep the guest's identity (disk, ports,
// credentials) and swap only the boot environment — and it must not share a
// VM name with the build boot, or the QMP socket paths collide.
func TestFinalizeSpec_DerivesEL3BootFromBuildSpec(t *testing.T) {
	build := Spec{
		VMName:       "devcell-qemu-build",
		CPUs:         4,
		MemoryGB:     6,
		DiskPath:     "/x/disk.qcow2",
		FirmwarePath: "/usr/share/qemu/edk2-aarch64-code.fd",
		VarsPath:     "/x/vars.fd",
		VirtioISO:    "/x/virtio.iso",
		SSHHost:      "127.0.0.1",
		SSHPort:      10022,
		SSHUser:      "dmitry",
		SSHKeyPath:   "/k/id_ed25519",
		MACAddr:      "52:54:00:00:00:01",
		QMPSocketDir: "/x",
		NestedVirt:   true,
	}

	fin := FinalizeSpec(build, "/cache/QEMU_EFI.kernel.fd")

	// Same guest, same access.
	assert.Equal(t, build.DiskPath, fin.DiskPath)
	assert.Equal(t, build.SSHPort, fin.SSHPort)
	assert.Equal(t, build.SSHUser, fin.SSHUser)
	assert.Equal(t, build.SSHKeyPath, fin.SSHKeyPath)
	assert.Equal(t, build.MACAddr, fin.MACAddr)

	// New boot environment: EL3 via -kernel, no pflash vars bank, no NestedVirt
	// (the secure machine supersedes it), no install media.
	assert.True(t, fin.SecureWorld)
	assert.True(t, fin.FirmwareKernel)
	assert.Equal(t, "/cache/QEMU_EFI.kernel.fd", fin.FirmwarePath)
	assert.Empty(t, fin.VarsPath, "-kernel loading has no pflash NVRAM bank")
	assert.False(t, fin.NestedVirt, "SecureWorld machine replaces the NestedVirt one")
	assert.Empty(t, fin.VirtioISO, "install media has no business in the finalize boot")

	assert.NotEqual(t, build.VMName, fin.VMName,
		"a distinct VM name keeps QMP/pid paths from colliding with the build boot")
}

// End to end through the command builder: the finalize spec must emit exactly
// the proven WSL2 machine line.
func TestFinalizeSpec_ArgvIsTheProvenWSL2Machine(t *testing.T) {
	build := Spec{
		VMName:   "devcell-qemu-build",
		CPUs:     4,
		MemoryGB: 6,
		DiskPath: "/x/disk.qcow2",
		SSHPort:  10022,
	}
	fin := FinalizeSpec(build, "/cache/QEMU_EFI.kernel.fd")
	fin.ApplyDefaults()
	joined := strings.Join(BuildRunCommand(fin), " ")

	assert.Contains(t, joined, "secure=on")
	assert.Contains(t, joined, "-cpu neoverse-n1")
	assert.Contains(t, joined, "-kernel /cache/QEMU_EFI.kernel.fd")
	assert.NotContains(t, joined, "if=pflash")
}

// The host side of the project share. The binary comes from
// DEVCELL_VIRTIOFSD or PATH; the command must expose the shared dir on the
// socket the spec points at, with --sandbox none (the container has no user
// namespaces for virtiofsd's default sandbox).
func TestVirtiofsdCommand_SharesTheProjectOnTheSocket(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "virtiofsd")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("DEVCELL_VIRTIOFSD", bin)

	resolved, err := VirtiofsdPath()
	require.NoError(t, err)
	assert.Equal(t, bin, resolved)

	cmd := VirtiofsdCommand(resolved, "/tmp/fs.sock", "/repo")
	joined := strings.Join(cmd.Args, " ")
	assert.Contains(t, joined, "--socket-path /tmp/fs.sock")
	assert.Contains(t, joined, "--shared-dir /repo")
	assert.Contains(t, joined, "--sandbox none")
}

func TestVirtiofsdPath_ErrorNamesTheKnob(t *testing.T) {
	t.Setenv("DEVCELL_VIRTIOFSD", "")
	t.Setenv("PATH", t.TempDir())
	_, err := VirtiofsdPath()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEVCELL_VIRTIOFSD",
		"the error must say how to point at a binary")
}
