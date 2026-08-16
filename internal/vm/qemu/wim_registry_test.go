package qemu

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/goregedit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHyperVBootPatches_ContainsAllRequiredPatches(t *testing.T) {
	rp := HyperVBootPatches()

	assert.Equal(t, `\Windows\System32\config\SYSTEM`, rp.HivePath)

	// Every service that exists in boot.wim's SYSTEM hive and needs a
	// Start value change for Hyper-V to initialize in WinPE.
	//
	// Source: SYSTEM.reg extracted in TestRegistryExtraction (2026-08-14).
	expected := []goregedit.DWordPatch{
		// Kernel drivers — must load at boot (Start=0).
		{KeyPath: `ControlSet001\Services\hvservice`, ValueName: "Start", Value: 0},
		{KeyPath: `ControlSet001\Services\vmbusr`, ValueName: "Start", Value: 0},

		// vmbus has Start=0 but StartOverride\0=3 downgrades it to Manual.
		{KeyPath: `ControlSet001\Services\vmbus\StartOverride`, ValueName: "0", Value: 0},

		// Win32 services — Auto (2) so SCM starts them in WinPE.
		{KeyPath: `ControlSet001\Services\HvHost`, ValueName: "Start", Value: 2},
		{KeyPath: `ControlSet001\Services\vmcompute`, ValueName: "Start", Value: 2},
	}

	require.Len(t, rp.Patches, len(expected), "patch count mismatch")

	for _, exp := range expected {
		found := false
		for _, got := range rp.Patches {
			if got.KeyPath == exp.KeyPath && got.ValueName == exp.ValueName {
				found = true
				assert.Equal(t, exp.Value, got.Value,
					"wrong value for %s\\%s", exp.KeyPath, exp.ValueName)
				break
			}
		}
		assert.True(t, found, "missing patch: %s\\%s", exp.KeyPath, exp.ValueName)
	}
}

func TestHyperVBootChecks_MatchesPatches(t *testing.T) {
	patches := HyperVBootPatches().Patches
	checks := HyperVBootChecks()

	require.Len(t, checks, len(patches), "checks and patches must cover the same entries")

	for i, p := range patches {
		c := checks[i]
		assert.Equal(t, p.KeyPath, c.KeyPath, "check[%d] KeyPath", i)
		assert.Equal(t, p.ValueName, c.ValueName, "check[%d] ValueName", i)
		assert.Equal(t, p.Value, c.Expected, "check[%d] Expected value", i)
	}
}
