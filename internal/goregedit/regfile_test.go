package goregedit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A .reg export is the transplant's source of truth: install.wim's hive
// only carries the services that ship enabled, while the rest are created
// when the feature is turned on. Exports come from a reference machine
// with VirtualMachinePlatform already enabled.

const sampleReg = `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\vmbusr]
"ImagePath"=hex(2):5c,00,53,00,79,00,73,00,00,00
"Type"=dword:00000001
"Start"=dword:00000003
"Group"="Extended Base"
"Owners"=hex(7):77,00,76,00,00,00,00,00

[HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\vmbusr\Parameters]
"Flags"=dword:00000002

[HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\vmbusr\Parameters\Wdf]
"Blob"=hex:de,ad,be,ef
`

func TestParseRegExport_TopLevelValues(t *testing.T) {
	keys, err := ParseRegExport(strings.NewReader(sampleReg))
	require.NoError(t, err)

	key, ok := keys[`SYSTEM\CurrentControlSet\Services\vmbusr`]
	require.True(t, ok, "export must yield the service key by hive-relative path")

	assert.Equal(t, "vmbusr", key.Name)
	assert.Equal(t, uint32(1), key.Values["Type"].DWord())
	assert.Equal(t, uint32(3), key.Values["Start"].DWord())

	assert.Equal(t, TypeString, key.Values["Group"].Type)
	assert.Equal(t, "Extended Base", key.Values["Group"].String())

	assert.Equal(t, TypeExpandString, key.Values["ImagePath"].Type)
	assert.Equal(t, `\Sys`, key.Values["ImagePath"].String())

	assert.Equal(t, TypeMultiString, key.Values["Owners"].Type)
	assert.Equal(t, []string{"wv"}, key.Values["Owners"].Strings())
}

func TestParseRegExport_NestsSubkeys(t *testing.T) {
	keys, err := ParseRegExport(strings.NewReader(sampleReg))
	require.NoError(t, err)

	key := keys[`SYSTEM\CurrentControlSet\Services\vmbusr`]
	require.Contains(t, key.Subkeys, "Parameters")

	params := key.Subkeys["Parameters"]
	assert.Equal(t, uint32(2), params.Values["Flags"].DWord())

	require.Contains(t, params.Subkeys, "Wdf")
	assert.Equal(t, TypeBinary, params.Subkeys["Wdf"].Values["Blob"].Type)
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, params.Subkeys["Wdf"].Values["Blob"].Data)
}

func TestParseRegExport_ContinuationLines(t *testing.T) {
	const wrapped = `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\x]
"ImagePath"=hex(2):5c,00,53,00,79,00,73,00,74,00,65,00,6d,00,52,00,6f,00,6f,00,\
  74,00,00,00
`
	keys, err := ParseRegExport(strings.NewReader(wrapped))
	require.NoError(t, err)

	key := keys[`SYSTEM\CurrentControlSet\Services\x`]
	assert.Equal(t, `\SystemRoot`, key.Values["ImagePath"].String(),
		"backslash-continued hex lines must be joined")
}
