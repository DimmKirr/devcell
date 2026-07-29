//go:build !wimlib

package qemu

import "testing"

func assembleISOFromESD(t *testing.T, esdPath, isoPath string) {
	t.Helper()
	t.Skip("wimlib build tag required to assemble ISO from ESD")
}
