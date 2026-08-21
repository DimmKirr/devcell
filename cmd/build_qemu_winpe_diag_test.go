package main

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
	"github.com/stretchr/testify/assert"
)

// DEVCELL_QEMU_WINPE_AGENT=1 (set by `task debug:autobuild`) ships the WinPE
// agent and a one-shot diagnostic command whose output the agent writes to
// devcell-out.txt on the answer volume — the only look inside a WinPE where
// the $WinPEDriver$ vioscsi load may have failed silently (CELL-429, run
// 20260812T141319).
func TestWinPEAgentDebugEnabled(t *testing.T) {
	assert.True(t, winpeAgentDebugEnabled(func(k string) string {
		if k == "DEVCELL_QEMU_WINPE_AGENT" {
			return "1"
		}
		return ""
	}))
	assert.False(t, winpeAgentDebugEnabled(func(string) string { return "" }))
}

func TestWinPEDiagCommand_ReadOnlyDiagnostics(t *testing.T) {
	// Read-only on purpose: run 20260812T143146 died with 0x80070103
	// (ERROR_NO_MORE_ITEMS — driver already loaded) after the agent's diag
	// drvloaded the same INF that wpeinit had already picked up from
	// $WinPEDriver$. The diagnostic must observe, never mutate.
	assert.NotContains(t, qemu.WinPEDiagCommand, "drvload", "diag must not load drivers: it caused the double-load abort")
	assert.Contains(t, qemu.WinPEDiagCommand, "diskpart.exe")
	assert.Contains(t, qemu.WinPEDiagCommand, `reg.exe query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi`)
	assert.Contains(t, qemu.WinPEDiagCommand, "Panther")
	assert.False(t, strings.Contains(qemu.WinPEDiagCommand, "\n"), "must stay a single line: the agent reads the first line via Get-Content")
}
