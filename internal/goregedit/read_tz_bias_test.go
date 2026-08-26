//go:build wimlib

package goregedit

import (
	"os"
	"testing"
)

func TestReadTZBias(t *testing.T) {
	hive := os.Getenv("HIVE_PATH")
	if hive == "" {
		t.Skip("HIVE_PATH not set")
	}

	val, err := ReadDWord(hive, `ControlSet001\Control\TimeZoneInformation`, "Bias")
	if err != nil {
		t.Logf("Bias (dword): error: %v", err)
	} else {
		t.Logf("Bias (dword): %d (0x%x) → %d minutes", val, val, int32(val))
	}

	val, err = ReadDWord(hive, `ControlSet001\Control\TimeZoneInformation`, "ActiveTimeBias")
	if err != nil {
		t.Logf("ActiveTimeBias: error: %v", err)
	} else {
		t.Logf("ActiveTimeBias: %d (0x%x) → %d minutes", val, val, int32(val))
	}

	val, err = ReadDWord(hive, `ControlSet001\Control\TimeZoneInformation`, "RealTimeIsUniversal")
	if err != nil {
		t.Logf("RealTimeIsUniversal: error: %v", err)
	} else {
		t.Logf("RealTimeIsUniversal: %d", val)
	}
}
