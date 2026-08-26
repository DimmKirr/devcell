package goregedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hiveFixture returns a SYSTEM hive to read. The fixture is produced by
// extracting Windows/System32/config/SYSTEM from install.wim (see
// internal/vm/qemu transplant tests); when absent the test skips so the
// package stays testable without a Windows ISO on hand.
func hiveFixture(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		filepath.Join("testdata", "SYSTEM"),
		filepath.Join("..", "..", ".scratch", "cell-434", "install-SYSTEM"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no SYSTEM hive fixture available")
	return ""
}

func TestReadServiceKey_HvService(t *testing.T) {
	hive := hiveFixture(t)

	key, err := ReadServiceKey(hive, `ControlSet001\Services\hvservice`)
	require.NoError(t, err)

	assert.Equal(t, "hvservice", key.Name)

	imagePath, ok := key.Values["ImagePath"]
	require.True(t, ok, "hvservice must carry ImagePath")
	assert.Equal(t, TypeExpandString, imagePath.Type)
	assert.Contains(t, imagePath.String(), "hvservice.sys")

	typ, ok := key.Values["Type"]
	require.True(t, ok, "hvservice must carry Type")
	assert.Equal(t, TypeDWord, typ.Type)
	assert.Equal(t, uint32(1), typ.DWord(), "kernel driver")

	_, ok = key.Values["Start"]
	assert.True(t, ok, "hvservice must carry Start")
}

func TestReadServiceKey_MultiSzAndSubkeys(t *testing.T) {
	hive := hiveFixture(t)

	key, err := ReadServiceKey(hive, `ControlSet001\Services\vmbus`)
	require.NoError(t, err)

	// Owners is REG_MULTI_SZ — exercises multi-string decoding.
	if owners, ok := key.Values["Owners"]; ok {
		assert.Equal(t, TypeMultiString, owners.Type)
		assert.NotEmpty(t, owners.Strings(), "MULTI_SZ must decode to entries")
	}

	// vmbus has Parameters\Winsock with REG_BINARY values nested two deep.
	require.Contains(t, key.Subkeys, "Parameters", "vmbus must expose Parameters subtree")
	params := key.Subkeys["Parameters"]
	require.Contains(t, params.Subkeys, "Winsock", "Parameters must expose Winsock")

	winsock := params.Subkeys["Winsock"]
	guid, ok := winsock.Values["ProviderGUID"]
	require.True(t, ok, "Winsock must carry ProviderGUID")
	assert.Equal(t, TypeBinary, guid.Type)
	assert.Len(t, guid.Data, 16, "a GUID is 16 raw bytes")
}

func TestReadServiceKey_Missing(t *testing.T) {
	hive := hiveFixture(t)

	_, err := ReadServiceKey(hive, `ControlSet001\Services\definitely-not-a-service`)
	require.Error(t, err, "reading an absent service must fail")
}
