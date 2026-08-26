package goregedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The hypervisor only starts if the boot loader entry says so. WinPE boots
// from a ramdisk and cannot reach its own BCD store (bcdedit /enum fails
// with "boot configuration data store could not be opened"), so the switch
// has to be flipped on the host while the boot media is being staged.
//
// BCD is itself a registry hive: Objects\{guid}\Elements\<code>\Element.

func copyBCD(t *testing.T) string {
	t.Helper()

	src := filepath.Join("testdata", "BCD")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skip("no BCD fixture available")
	}
	dst := filepath.Join(t.TempDir(), "BCD")
	require.NoError(t, os.WriteFile(dst, data, 0644))
	return dst
}

func TestSetHypervisorLaunchType_CreatesElement(t *testing.T) {
	bcd := copyBCD(t)

	require.NoError(t, SetHypervisorLaunchType(bcd, HypervisorLaunchAuto))

	key, err := ReadServiceKey(bcd,
		`Objects\`+WinPELoaderGUID+`\Elements\`+ElementHypervisorLaunchType)
	require.NoError(t, err, "the element must exist after patching")

	elem, ok := key.Values["Element"]
	require.True(t, ok, "BCD elements carry their payload in a value named Element")
	assert.Equal(t, TypeBinary, elem.Type)
	assert.Equal(t, []byte{1, 0, 0, 0, 0, 0, 0, 0}, elem.Data,
		"integer elements are 8-byte little-endian; Auto is 1")
}

func TestSetHypervisorLaunchType_Off(t *testing.T) {
	bcd := copyBCD(t)

	require.NoError(t, SetHypervisorLaunchType(bcd, HypervisorLaunchOff))

	key, err := ReadServiceKey(bcd,
		`Objects\`+WinPELoaderGUID+`\Elements\`+ElementHypervisorLaunchType)
	require.NoError(t, err)
	assert.Equal(t, []byte{0, 0, 0, 0, 0, 0, 0, 0}, key.Values["Element"].Data)
}

func TestSetHypervisorLaunchType_LeavesLoaderIntact(t *testing.T) {
	bcd := copyBCD(t)

	before, err := ReadServiceKey(bcd, `Objects\`+WinPELoaderGUID+`\Elements\12000002`)
	require.NoError(t, err, "fixture must have the loader application path")

	require.NoError(t, SetHypervisorLaunchType(bcd, HypervisorLaunchAuto))

	after, err := ReadServiceKey(bcd, `Objects\`+WinPELoaderGUID+`\Elements\12000002`)
	require.NoError(t, err, "the loader's other elements must survive")
	assert.Equal(t, before.Values["Element"].Data, after.Values["Element"].Data)
}

func TestClearWinPEFlag(t *testing.T) {
	bcd := copyBCD(t)

	// Stock BCD has WinPE=1; verify it exists first.
	before, err := ReadServiceKey(bcd,
		`Objects\`+WinPELoaderGUID+`\Elements\`+ElementWinPE)
	require.NoError(t, err, "fixture must have the WinPE element")
	assert.Equal(t, []byte{0x01}, before.Values["Element"].Data,
		"stock WinPE BCD must have winpe=1")

	require.NoError(t, ClearWinPEFlag(bcd))

	after, err := ReadServiceKey(bcd,
		`Objects\`+WinPELoaderGUID+`\Elements\`+ElementWinPE)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x00}, after.Values["Element"].Data,
		"after clearing, winpe must be 0")
}

func TestClearWinPEFlag_LeavesHypervisorLaunchIntact(t *testing.T) {
	bcd := copyBCD(t)

	require.NoError(t, SetHypervisorLaunchType(bcd, HypervisorLaunchAuto))
	require.NoError(t, ClearWinPEFlag(bcd))

	key, err := ReadServiceKey(bcd,
		`Objects\`+WinPELoaderGUID+`\Elements\`+ElementHypervisorLaunchType)
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 0, 0, 0, 0, 0, 0, 0}, key.Values["Element"].Data,
		"hypervisorlaunchtype=Auto must survive WinPE flag clearing")
}

func TestSetHypervisorLaunchType_HiveStaysValid(t *testing.T) {
	bcd := copyBCD(t)

	require.NoError(t, SetHypervisorLaunchType(bcd, HypervisorLaunchAuto))

	out := runHivexGet(t, bcd,
		`\Objects\`+WinPELoaderGUID+`\Elements\`+ElementHypervisorLaunchType, "Element")
	assert.NotEmpty(t, out, "hivex must read the element we created")
}
