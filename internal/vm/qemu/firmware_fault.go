package qemu

import (
	"fmt"
	"strconv"
	"strings"
)

// edk2Banner is printed once per firmware start. Counting it is the cheapest
// reliable way to notice the guest reset: a screendump cannot tell "back at
// the firmware splash" from "never left it".
const edk2Banner = "UEFI firmware (version"

// FirmwareBootCount reports how many times the guest firmware started.
// More than one during an install that has not finished applying its image
// means the guest reset prematurely.
func FirmwareBootCount(serial string) int {
	return strings.Count(serial, edk2Banner)
}

// FirmwareFault is EDK2's CPU exception dump, which it prints to the serial
// console before giving up. Its presence means the *firmware* died — not the
// guest OS — so no amount of further waiting can help.
type FirmwareFault struct {
	SP          string
	ELR         string
	ESR         string
	FAR         string
	Description string // e.g. "Data abort: Translation fault, second level"
}

// ParseFirmwareFault extracts the crash dump from a serial log, if present.
func ParseFirmwareFault(serial string) (FirmwareFault, bool) {
	f := FirmwareFault{
		SP:  fieldAfter(serial, "SP 0x"),
		ELR: fieldAfter(serial, "ELR 0x"),
		ESR: fieldAfter(serial, "ESR 0x"),
		FAR: fieldAfter(serial, "FAR 0x"),
	}
	for _, line := range strings.Split(serial, "\n") {
		l := strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if strings.HasPrefix(l, "Data abort:") ||
			strings.HasPrefix(l, "Prefetch abort:") ||
			strings.HasPrefix(l, "Synchronous Exception") {
			f.Description = l
			break
		}
	}
	if f.ESR == "" || f.FAR == "" {
		return FirmwareFault{}, false
	}
	return f, true
}

// fieldAfter returns the hex value following a "NAME 0x" marker, normalised
// back to a 0x-prefixed string. EDK2 pads these fields with varying
// whitespace, so the marker carries its own "0x".
func fieldAfter(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i+len(marker):]
	end := 0
	for end < len(rest) && isHexDigit(rest[end]) {
		end++
	}
	if end == 0 {
		return ""
	}
	return "0x" + rest[:end]
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// Summary interprets the dump in one line.
//
// The load-bearing observation for the 2026-07-30 install failure: the
// faulting address sits just *below* the stack pointer, and the stack pointer
// sits at the very bottom of guest RAM (QEMU virt puts RAM at 0x40000000, with
// the PCIe ECAM window immediately below it). That is a stack that ran off the
// bottom of its region — not a stray pointer.
func (f FirmwareFault) Summary() string {
	base := fmt.Sprintf("EDK2 %s (ESR=%s FAR=%s ELR=%s SP=%s)",
		orUnknown(f.Description), f.ESR, f.FAR, f.ELR, f.SP)

	sp, spErr := strconv.ParseUint(strings.TrimPrefix(f.SP, "0x"), 16, 64)
	far, farErr := strconv.ParseUint(strings.TrimPrefix(f.FAR, "0x"), 16, 64)
	if spErr != nil || farErr != nil || far >= sp {
		return base
	}
	return base + fmt.Sprintf(
		" — the faulting address is 0x%x below the SP, so the firmware overran its stack; "+
			"guest RAM starts at 0x40000000 and the PCIe ECAM window sits directly below it",
		sp-far)
}

func orUnknown(s string) string {
	if s == "" {
		return "CPU exception"
	}
	return s
}
