package main

import (
	"testing"

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
