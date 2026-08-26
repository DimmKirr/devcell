package goregedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copyHive gives each test its own scratch copy so writes never touch the
// fixture.
func copyHive(t *testing.T) string {
	t.Helper()
	src := hiveFixture(t)
	data, err := os.ReadFile(src)
	require.NoError(t, err)

	dst := filepath.Join(t.TempDir(), "SYSTEM")
	require.NoError(t, os.WriteFile(dst, data, 0644))
	return dst
}

func TestWriteKey_CreatesNewServiceWithValues(t *testing.T) {
	hive := copyHive(t)

	// vmbusr is absent from install.wim's hive — CBS only creates it when
	// VirtualMachinePlatform is enabled. This is the transplant's core case.
	spec := &Key{
		Name: "vmbusr",
		Values: map[string]Value{
			"ImagePath":    {Type: TypeExpandString, Data: encodeUTF16(`\SystemRoot\System32\drivers\vmbusr.sys`)},
			"Type":         {Type: TypeDWord, Data: []byte{1, 0, 0, 0}},
			"Start":        {Type: TypeDWord, Data: []byte{3, 0, 0, 0}},
			"ErrorControl": {Type: TypeDWord, Data: []byte{1, 0, 0, 0}},
			"Group":        {Type: TypeString, Data: encodeUTF16("Extended Base")},
			"Owners":       {Type: TypeMultiString, Data: encodeUTF16("wvmbusr.inf\x00")},
		},
		Subkeys: map[string]*Key{},
	}

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\vmbusr`, spec))

	got, err := ReadServiceKey(hive, `ControlSet001\Services\vmbusr`)
	require.NoError(t, err, "the new key must be readable back")

	assert.Equal(t, "vmbusr", got.Name)
	assert.Equal(t, `\SystemRoot\System32\drivers\vmbusr.sys`, got.Values["ImagePath"].String())
	assert.Equal(t, uint32(1), got.Values["Type"].DWord())
	assert.Equal(t, uint32(3), got.Values["Start"].DWord())
	assert.Equal(t, "Extended Base", got.Values["Group"].String())
	assert.Equal(t, []string{"wvmbusr.inf"}, got.Values["Owners"].Strings())
}

func TestWriteKey_CreatesSubkeyTree(t *testing.T) {
	hive := copyHive(t)

	spec := &Key{
		Name:   "VmsProxy",
		Values: map[string]Value{"Start": {Type: TypeDWord, Data: []byte{0, 0, 0, 0}}},
		Subkeys: map[string]*Key{
			"parameters": {
				Name:    "parameters",
				Values:  map[string]Value{"Flags": {Type: TypeDWord, Data: []byte{2, 0, 0, 0}}},
				Subkeys: map[string]*Key{},
			},
			"SharedState": {
				Name: "SharedState",
				Values: map[string]Value{
					"Blob": {Type: TypeBinary, Data: []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02}},
				},
				Subkeys: map[string]*Key{},
			},
		},
	}

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\VmsProxy`, spec))

	got, err := ReadServiceKey(hive, `ControlSet001\Services\VmsProxy`)
	require.NoError(t, err)

	require.Contains(t, got.Subkeys, "parameters")
	assert.Equal(t, uint32(2), got.Subkeys["parameters"].Values["Flags"].DWord())

	require.Contains(t, got.Subkeys, "SharedState")
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02},
		got.Subkeys["SharedState"].Values["Blob"].Data,
		"REG_BINARY must round-trip byte for byte")
}

func TestWriteKey_PreservesExistingKeys(t *testing.T) {
	hive := copyHive(t)

	before, err := ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\vmbusr`, &Key{
		Name:    "vmbusr",
		Values:  map[string]Value{"Start": {Type: TypeDWord, Data: []byte{3, 0, 0, 0}}},
		Subkeys: map[string]*Key{},
	}))

	after, err := ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err, "unrelated keys must survive the write")
	assert.Equal(t, before.Values["ImagePath"].String(), after.Values["ImagePath"].String())
	assert.Equal(t, before.Values["Start"].DWord(), after.Values["Start"].DWord())
}

func TestWriteKey_OverwritesExistingValues(t *testing.T) {
	hive := copyHive(t)

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\hvservice`, &Key{
		Name:    "hvservice",
		Values:  map[string]Value{"Start": {Type: TypeDWord, Data: []byte{0, 0, 0, 0}}},
		Subkeys: map[string]*Key{},
	}))

	got, err := ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), got.Values["Start"].DWord(), "Start must be updated to Boot")
	assert.Contains(t, got.Values["ImagePath"].String(), "hvservice.sys",
		"values not in the spec must be left alone")
}

func TestWriteKey_HiveStaysValidForHivex(t *testing.T) {
	hive := copyHive(t)

	require.NoError(t, WriteKey(hive, `ControlSet001\Services\vmbusr`, &Key{
		Name: "vmbusr",
		Values: map[string]Value{
			"ImagePath": {Type: TypeExpandString, Data: encodeUTF16(`\SystemRoot\System32\drivers\vmbusr.sys`)},
			"Start":     {Type: TypeDWord, Data: []byte{3, 0, 0, 0}},
		},
		Subkeys: map[string]*Key{
			"Parameters": {Name: "Parameters", Values: map[string]Value{}, Subkeys: map[string]*Key{}},
		},
	}))

	// hivex is an independent implementation: if it can walk to our new key
	// and read the value back, the structure is sound, not just self-consistent.
	out := runHivexGet(t, hive, `\ControlSet001\Services\vmbusr`, "Start")
	assert.Contains(t, out, "3", "hivex must read the Start value we wrote")
}
