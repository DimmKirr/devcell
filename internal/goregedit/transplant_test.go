package goregedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end: take the real VirtualMachinePlatform service export captured
// from a reference machine and clone every key into a SYSTEM hive, then
// verify both with our own reader and with hivex.

func loadVMPExport(t *testing.T) map[string]*Key {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", "vmp-services.reg"))
	require.NoError(t, err)
	defer f.Close()

	keys, err := ParseRegExport(f)
	require.NoError(t, err)
	return keys
}

func TestVMPExport_CoversAllTwelveServices(t *testing.T) {
	keys := loadVMPExport(t)

	want := []string{
		"vmbus", "vmbusr", "vmbusproxy", "hvservice", "hvcrash",
		"hvsocketcontrol", "vmgid", "VMSP", "VmsProxy", "VMSNPXY",
		"vmcompute", "HvHost",
	}
	for _, name := range want {
		_, ok := keys[`SYSTEM\CurrentControlSet\Services\`+name]
		assert.True(t, ok, "export must define %s", name)
	}
}

func TestTransplantVMPServices_IntoHive(t *testing.T) {
	hive := copyHive(t)
	keys := loadVMPExport(t)

	for _, name := range []string{
		"vmbus", "vmbusr", "vmbusproxy", "hvservice", "hvcrash",
		"hvsocketcontrol", "vmgid", "VMSP", "VmsProxy", "VMSNPXY",
		"vmcompute", "HvHost",
	} {
		spec := keys[`SYSTEM\CurrentControlSet\Services\`+name]
		require.NotNil(t, spec, "export must define %s", name)
		require.NoError(t, WriteKey(hive, `ControlSet001\Services\`+name, spec),
			"cloning %s must succeed", name)
	}

	// Every service must read back with the essentials SCM needs.
	for _, name := range []string{
		"vmbus", "vmbusr", "vmbusproxy", "hvservice", "hvcrash",
		"hvsocketcontrol", "vmgid", "VMSP", "VmsProxy", "VMSNPXY",
		"vmcompute", "HvHost",
	} {
		got, err := ReadServiceKey(hive, `ControlSet001\Services\`+name)
		require.NoError(t, err, "%s must be readable after transplant", name)

		imagePath, ok := got.Values["ImagePath"]
		require.True(t, ok, "%s must carry ImagePath", name)
		assert.NotEmpty(t, imagePath.String(), "%s ImagePath must decode", name)

		_, ok = got.Values["Start"]
		assert.True(t, ok, "%s must carry Start", name)
	}

	// Spot-check a nested subtree survived with its binary payload.
	vmbus, err := ReadServiceKey(hive, `ControlSet001\Services\vmbus`)
	require.NoError(t, err)
	require.Contains(t, vmbus.Subkeys, "Parameters")
	require.Contains(t, vmbus.Subkeys["Parameters"].Subkeys, "Winsock")
	guid := vmbus.Subkeys["Parameters"].Subkeys["Winsock"].Values["ProviderGUID"]
	assert.Equal(t, TypeBinary, guid.Type)
	assert.Len(t, guid.Data, 16)

	// hivex must accept the result — an independent structural check.
	assert.NotEmpty(t,
		runHivexGet(t, hive, `\ControlSet001\Services\vmcompute`, "ImagePath"),
		"hivex must read a transplanted service")
}

func TestTransplantVMPServices_BootStartOverrides(t *testing.T) {
	hive := copyHive(t)
	keys := loadVMPExport(t)

	spec := keys[`SYSTEM\CurrentControlSet\Services\hvservice`]
	require.NotNil(t, spec)
	// WinPE must load the hypervisor driver at boot, not on demand.
	spec.Values["Start"] = Value{Type: TypeDWord, Data: []byte{0, 0, 0, 0}}

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\hvservice`, spec))

	got, err := ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), got.Values["Start"].DWord())
}
