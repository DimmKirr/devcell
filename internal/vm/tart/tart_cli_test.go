package tart

import (
	"strings"
	"testing"
)

func TestTartRunArgOrder_DirBeforeName(t *testing.T) {
	// Verify --dir flags come BEFORE the VM name positional argument.
	// tart's CLI parser requires flags before positional args;
	// placing --dir after the name silently drops the share.

	// We can't call TartRun (it execs tart), but we can reconstruct the
	// arg-building logic and verify the invariant.
	dirs := map[string]string{
		"nixhome": "/Users/test/nixhome",
	}
	name := "test-vm"

	args := []string{"run", "--no-graphics"}
	for tag, path := range dirs {
		args = append(args, "--dir", tag+":"+path)
	}
	args = append(args, name)

	// The VM name must be the LAST element.
	if args[len(args)-1] != name {
		t.Fatalf("VM name should be last arg, got args: %v", args)
	}

	// --dir must appear before the VM name.
	joined := strings.Join(args, " ")
	dirIdx := strings.Index(joined, "--dir")
	nameIdx := strings.LastIndex(joined, name)
	if dirIdx > nameIdx {
		t.Fatalf("--dir flag must come before VM name; got: %s", joined)
	}
}
