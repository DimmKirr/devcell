//go:build wimlib

package wimlib

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDCS(t *testing.T) {
	assert.True(t, IsDCS([]byte("DCS\x01rest")))
	assert.False(t, IsDCS([]byte("MZ\x90\x00")))
	assert.False(t, IsDCS([]byte("DC")))
}

func TestDecompressDCS_BadSignature(t *testing.T) {
	_, err := DecompressDCS([]byte("MZ\x90\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad signature")
}

func TestDecompressDCS_TooShort(t *testing.T) {
	_, err := DecompressDCS([]byte("DCS\x01"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestDecompressDCS_RealFile(t *testing.T) {
	const dcsPath = "/tmp/dcs-testdata/vmfirmware.dll.dcs"
	const refPath = "/tmp/dcs-testdata/vmfirmware.dll.ref"
	const refSHA256 = "06007f981919d4a6df271e82251aab47b496dff2a60726bc2d78a1b7247f11ad"

	data, err := os.ReadFile(dcsPath)
	if err != nil {
		t.Skipf("DCS test file not available: %v", err)
	}

	result, err := DecompressDCS(data)
	require.NoError(t, err)

	h := sha256.Sum256(result)
	got := hex.EncodeToString(h[:])
	assert.Equal(t, refSHA256, got, "decompressed SHA256 must match reference")

	// Must be a valid PE
	require.GreaterOrEqual(t, len(result), 2)
	assert.Equal(t, "MZ", string(result[:2]), "decompressed file must be a PE")

	// Cross-check with reference file if available
	ref, err := os.ReadFile(refPath)
	if err == nil {
		assert.Equal(t, ref, result, "decompressed output must exactly match C reference")
	}
}
