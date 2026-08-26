// Package hcsvm builds Host Compute System documents for the nested-VM
// smoke test. It is imported both by the qemu test harness (any platform)
// and by the hcsboot tool that runs inside WinPE (windows/arm64), so it
// must stay free of platform-specific imports.
package hcsvm

import (
	"encoding/json"
	"fmt"
)

// VMDocJSON returns an HCS schema 2.1 VirtualMachine document for a
// diskless Gen2 VM. With no boot device the guest UEFI (vmfirmware.dll)
// runs to its boot screen and stays there — exactly enough to prove the
// whole vmcompute → vmwp → Vid → hypervisor path works.
func VMDocJSON(memMB, cpus int) string {
	doc := map[string]any{
		"SchemaVersion":                     map[string]int{"Major": 2, "Minor": 1},
		"Owner":                             "devcell",
		"ShouldTerminateOnLastHandleClosed": true,
		"VirtualMachine": map[string]any{
			"StopOnReset": true,
			"Chipset":     map[string]any{"Uefi": map[string]any{}},
			"ComputeTopology": map[string]any{
				"Memory":    map[string]any{"SizeInMB": memMB},
				"Processor": map[string]any{"Count": cpus},
			},
		},
	}
	out, err := json.Marshal(doc)
	if err != nil {
		panic(fmt.Sprintf("marshalling HCS doc: %v", err))
	}
	return string(out)
}
