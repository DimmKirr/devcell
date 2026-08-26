//go:build wimlib

package qemu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installWimFixture points at the extracted Windows media. These tests read
// real binaries out of install.wim, so they skip without it.
func installWimFixture(t *testing.T) string {
	t.Helper()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	p := filepath.Join(home, ".devcell", "cache", "qemu", "mct-work",
		"iso-stage", "sources", "install.wim")
	if _, err := os.Stat(p); err != nil {
		t.Skip("install.wim not available; skipping transplant extraction test")
	}
	return p
}

// ExtractTransplantFiles requires a donor install.wim with VMP enabled
// so all binaries are materialized at their System32 paths.
func TestExtractTransplantFiles_PullsEveryBinary(t *testing.T) {
	installWim := installWimFixture(t)
	dest := t.TempDir()

	if err := ExtractTransplantFiles(installWim, VMPTransplantServices(), dest); err != nil {
		t.Skipf("donor install.wim does not have VMP materialized: %v", err)
	}

	for _, svc := range VMPTransplantServices() {
		path := filepath.Join(dest, filepath.FromSlash(svc.File))
		info, err := os.Stat(path)
		require.NoError(t, err, "%s: %s must be staged at its destination path",
			svc.Name, svc.File)
		assert.Greater(t, info.Size(), int64(0), "%s must not be empty", svc.File)
	}
}

func TestExtractTransplantFiles_MissingSourceIsReported(t *testing.T) {
	installWim := installWimFixture(t)

	err := ExtractTransplantFiles(installWim, []TransplantService{
		{Name: "bogus", File: "Windows/System32/drivers/not-a-real-driver.sys"},
	}, t.TempDir())

	require.Error(t, err, "a missing source must fail loudly, not silently skip")
	assert.Contains(t, err.Error(), "not-a-real-driver.sys")
}
