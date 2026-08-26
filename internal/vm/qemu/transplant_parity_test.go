package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVMPParityFiles_ContractShape(t *testing.T) {
	files := VMPParityFiles()
	require.NotEmpty(t, files)

	seen := map[string]bool{}
	for _, f := range files {
		assert.True(t, strings.HasPrefix(f.Dest, "Windows/"),
			"%s must be a Windows/-relative destination", f.Dest)
		assert.False(t, seen[f.Dest], "duplicate destination %s", f.Dest)
		seen[f.Dest] = true
	}

	// The worker process and the guest UEFI are the two files the whole
	// exercise exists for: their absence is how CELL-434 started.
	assert.True(t, seen["Windows/System32/vmwp.exe"])
	assert.True(t, seen["Windows/System32/vmfirmware.dll"])
}

func TestVMMSExtraFiles_ContractShape(t *testing.T) {
	files := VMMSExtraFiles()
	require.NotEmpty(t, files)

	dests := map[string]bool{}
	for _, f := range files {
		dests[f.Dest] = true
	}
	assert.True(t, dests["Windows/System32/vmms.exe"])
	assert.True(t, dests["Windows/System32/VmDataStore.dll"])
	assert.True(t, dests["Windows/System32/vmmsprox.dll"])
	assert.True(t, dests["Windows/System32/WindowsVirtualization.V2.mof"])
}
