package goregedit

import (
	"encoding/binary"
	"fmt"
)

// A BCD store is a registry hive whose boot entries live under
// Objects\{guid}\Elements\<code>, each element holding its payload in a
// value named "Element".
const (
	// WinPELoaderGUID is the well-known identifier of the Windows PE boot
	// loader entry ({7619dcc9-fafe-11d9-b411-000476eba25f}) present in the
	// BCD store on Windows installation media.
	WinPELoaderGUID = "{7619dcc9-fafe-11d9-b411-000476eba25f}"

	// ElementHypervisorLaunchType selects whether winload starts the
	// hypervisor (BcdOSLoaderInteger_HypervisorLaunchType).
	ElementHypervisorLaunchType = "250000f0"

	// ElementWinPE is BcdOSLoaderBoolean_WinPE. When set to 1, winload
	// treats the boot as a WinPE session and skips subsystems that a
	// preinstallation environment does not need, including the hypervisor
	// launch path. Clearing it to 0 lets winload enter EL2 normally.
	ElementWinPE = "26000022"
)

// Hypervisor launch modes for ElementHypervisorLaunchType.
const (
	HypervisorLaunchOff  uint64 = 0
	HypervisorLaunchAuto uint64 = 1
)

// SetHypervisorLaunchType writes hypervisorlaunchtype into a BCD store's
// WinPE loader entry, creating the element when the media does not carry
// one — stock Windows media never does.
//
// This has to happen while the boot media is staged on the host: WinPE
// runs from a ramdisk and cannot open the BCD store it booted from.
func SetHypervisorLaunchType(bcdPath string, mode uint64) error {
	return SetBCDIntegerElement(bcdPath, WinPELoaderGUID, ElementHypervisorLaunchType, mode)
}

// SetBCDIntegerElement writes an 8-byte little-endian integer element into
// a BCD object, creating the element key if needed.
func SetBCDIntegerElement(bcdPath, objectGUID, elementCode string, value uint64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, value)

	keyPath := fmt.Sprintf(`Objects\%s\Elements\%s`, objectGUID, elementCode)

	return WriteKey(bcdPath, keyPath, &Key{
		Name: elementCode,
		Values: map[string]Value{
			"Element": {Type: TypeBinary, Data: payload},
		},
		Subkeys: map[string]*Key{},
	})
}

// SetBCDBooleanElement writes a 1-byte boolean element into a BCD object.
func SetBCDBooleanElement(bcdPath, objectGUID, elementCode string, value bool) error {
	var b byte
	if value {
		b = 1
	}

	keyPath := fmt.Sprintf(`Objects\%s\Elements\%s`, objectGUID, elementCode)

	return WriteKey(bcdPath, keyPath, &Key{
		Name: elementCode,
		Values: map[string]Value{
			"Element": {Type: TypeBinary, Data: []byte{b}},
		},
		Subkeys: map[string]*Key{},
	})
}

// ClearWinPEFlag sets BcdOSLoaderBoolean_WinPE to 0 on the WinPE loader
// entry. Stock WinPE media has this set to 1, which causes winload to
// skip the hypervisor launch path even when hypervisorlaunchtype=Auto.
func ClearWinPEFlag(bcdPath string) error {
	return SetBCDBooleanElement(bcdPath, WinPELoaderGUID, ElementWinPE, false)
}
