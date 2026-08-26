//go:build wimlib

package goregedit

import (
	"fmt"
	"os"
	"testing"
)

func TestDumpBCDElements(t *testing.T) {
	bcd := os.Getenv("BCD_PATH")
	if bcd == "" {
		t.Skip("BCD_PATH not set")
	}
	guid := "{7619dcc9-fafe-11d9-b411-000476eba25f}"

	elements := [][2]string{
		{"250000f0", "HypervisorLaunchType"},
		{"25000020", "NxPolicy"},
		{"26000022", "WinPE"},
		{"16000049", "AllowPrereleaseSignatures"},
		{"16000009", "DisableIntegrityChecks"},
		{"250000e3", "VSMLaunchType"},
		{"250000e1", "HypervisorDebugType"},
		{"25000143", "HypervisorEnforcedCodeIntegrity"},
		{"250000f4", "HypervisorMMIONXPolicy"},
		{"250000f2", "HypervisorSchedulerType"},
		{"250000f3", "HypervisorRootProcPerNode"},
	}

	for _, e := range elements {
		code, name := e[0], e[1]
		keyPath := fmt.Sprintf(`Objects\%s\Elements\%s`, guid, code)
		val, err := ReadDWord(bcd, keyPath, "Element")
		if err != nil {
			t.Logf("%-40s: not set", name+" ("+code+")")
		} else {
			t.Logf("%-40s: %d (0x%x)", name+" ("+code+")", val, val)
		}
	}
}
