package goregedit

import (
	"os/exec"
	"strings"
	"testing"
)

// runHivexGet reads a value with hivexget, an independent hive
// implementation (libguestfs). It validates our writes against a parser
// that shares none of our code. Skips when hivex is not installed.
func runHivexGet(t *testing.T, hivePath, keyPath, valueName string) string {
	t.Helper()

	bin, err := exec.LookPath("hivexget")
	if err != nil {
		t.Skip("hivexget not installed; skipping independent hive validation")
	}

	out, err := exec.Command(bin, hivePath, keyPath, valueName).CombinedOutput()
	if err != nil {
		t.Fatalf("hivexget %s %s %s failed: %v\n%s",
			hivePath, keyPath, valueName, err, out)
	}
	return strings.TrimSpace(string(out))
}
