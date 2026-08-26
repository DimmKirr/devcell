//go:build wimlib

package goregedit

import (
	"os"
	"testing"
)

func TestCheckTimeZone(t *testing.T) {
	hive := os.Getenv("HIVE_PATH")
	if hive == "" {
		t.Skip("HIVE_PATH not set")
	}

	// Try reading timezone info
	for _, path := range []string{
		`ControlSet001\Control\TimeZoneInformation`,
		`Select`,
	} {
		key, err := ReadServiceKey(hive, path)
		if err != nil {
			t.Logf("  %s: %v", path, err)
		} else {
			t.Logf("  %s:", path)
			for name, val := range key.Values {
				t.Logf("    %s = %v", name, val)
			}
			for name := range key.Subkeys {
				t.Logf("    [subkey] %s", name)
			}
		}
	}
}
