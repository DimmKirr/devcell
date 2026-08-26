//go:build wimlib

package wimlib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDecompressor_LZMS(t *testing.T) {
	d, err := NewDecompressor(LZMS, 1<<20)
	require.NoError(t, err)
	defer d.Close()
}

func TestNewDecompressor_InvalidType(t *testing.T) {
	_, err := NewDecompressor(None, 1<<20)
	require.Error(t, err)
}
