package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Firmware boot/fault parsing, against the real serial output of run
// 20260730T140237 — the install that reset ~4 minutes in with only 234MB
// written, then died in EDK2 on the second boot.

const twoBootsWithFault = `UEFI firmware (version edk2-stable202408-prebuilt.qemu.org built at 16:28:50 on Sep 12 2024)
ArmTrngLib could not be correctly initialized.
Tpm2SubmitCommand - Tcg2 - Not Found
BdsDxe: loading Boot0001 "UEFI QEMU QEMU USB HARDDRIVE 1-0000:00:03.0-3"
BdsDxe: starting Boot0001 "UEFI QEMU QEMU USB HARDDRIVE 1-0000:00:03.0-3"
UEFI firmware (version edk2-stable202408-prebuilt.qemu.org built at 16:28:50 on Sep 12 2024)
ArmTrngLib could not be correctly initialized.
BdsDxe: failed to load Boot0003 "UEFI QEMU NVMe Ctrl devcell0 1": Not Found
BdsDxe: starting Boot0001 "UEFI QEMU QEMU USB HARDDRIVE 1-0000:00:03.0-3"
  SP 0x0000000040000070  ELR 0x00000001BC266280  SPSR 0x60002749  FPSR 0x00000000
 ESR 0x96000046          FAR 0x000000003FFFFFD0

 ESR : EC 0x25  IL 0x1  ISS 0x00000046

Data abort: Translation fault, second level

Stack dump:

Recursive exception occurred while dumping the CPU state
`

func TestFirmwareBootCount(t *testing.T) {
	assert.Equal(t, 2, FirmwareBootCount(twoBootsWithFault),
		"two EDK2 banners means the guest reset once")
	assert.Equal(t, 0, FirmwareBootCount(""))
	assert.Equal(t, 1, FirmwareBootCount("UEFI firmware (version edk2-stable202408) \nBdsDxe: starting"))
}

func TestFirmwareFault_ParsesTheRealCrash(t *testing.T) {
	f, ok := ParseFirmwareFault(twoBootsWithFault)
	require.True(t, ok, "the EDK2 crash dump must be recognised")

	assert.Equal(t, "0x96000046", f.ESR)
	assert.Equal(t, "0x000000003FFFFFD0", f.FAR)
	assert.Equal(t, "0x00000001BC266280", f.ELR)
	assert.Equal(t, "0x0000000040000070", f.SP)
	assert.Contains(t, f.Description, "Translation fault")
}

// The interpretation that turns four hex numbers into the actual finding:
// SP sits just above RAM base and the faulting address is below it, so the
// firmware ran its stack off the bottom of RAM into the PCIe ECAM window.
func TestFirmwareFault_StackUnderflowInterpretation(t *testing.T) {
	f, ok := ParseFirmwareFault(twoBootsWithFault)
	require.True(t, ok)

	s := f.Summary()
	assert.Contains(t, s, "stack", "must name the stack as the mechanism")
	assert.Contains(t, s, "below the SP", "must state the fault is below the stack pointer")
	assert.Contains(t, s, "0xa0", "must quantify how far below")
}

func TestFirmwareFault_NoFaultInCleanLog(t *testing.T) {
	_, ok := ParseFirmwareFault("UEFI firmware (version edk2)\nBdsDxe: starting Boot0001\n")
	assert.False(t, ok)
}
