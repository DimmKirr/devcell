package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateWinPEShellINI_RunsBootstrapBeforeSetup(t *testing.T) {
	out := string(GenerateWinPEShellINI())
	assert.Contains(t, out, "[LaunchApps]")

	bootstrapIdx := strings.Index(out, WinPEBootstrapPath)
	setupIdx := strings.Index(out, "setup.exe")
	assert.Positive(t, bootstrapIdx, "bootstrap must be listed")
	assert.Positive(t, setupIdx, "setup.exe must still run")
	assert.Less(t, bootstrapIdx, setupIdx, "bootstrap must run before setup.exe")
}

func TestGenerateWinPEBootstrap_NoDriversByDefault(t *testing.T) {
	// CELL-359 made storage inbox-driver, so a default bootstrap loads none.
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	assert.NotContains(t, out, "drvload")
}

func TestGenerateWinPEBootstrap_LoadsRequestedDrivers(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{
		DriverINFs: []string{`X:\devcell\drivers\viostor.inf`, `X:\devcell\drivers\netkvm.inf`},
	}))
	assert.Contains(t, out, `drvload X:\devcell\drivers\viostor.inf`)
	assert.Contains(t, out, `drvload X:\devcell\drivers\netkvm.inf`)
}

func TestGenerateWinPEBootstrap_ReportsProgressToSerial(t *testing.T) {
	// The PCI 16550 from CELL-360 shows up as a COM port; writing to it is
	// how the guest reports progress the host can read as text.
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{ProgressPort: "COM1"}))
	assert.Contains(t, out, ">COM1")
	assert.Contains(t, out, "devcell:")
}

func TestGenerateWinPEBootstrap_StartsAgentDetached(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	// winpeshl runs entries synchronously — the agent must not block setup.exe
	assert.Contains(t, out, "start ")
	assert.Contains(t, out, WinPEAgentPath)
}

func TestGenerateWinPEAgent_PollsCommandFileAndWritesResult(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, AgentCommandFile, "must poll the command file")
	assert.Contains(t, out, AgentResultFile, "must write results back")
	assert.Contains(t, out, "del ", "must consume the command so it runs once")
}

func TestGenerateWinPEAgent_SearchesForTheCommandVolume(t *testing.T) {
	// Drive letters are not stable in WinPE, so the agent must locate the
	// devcell volume rather than hardcode one letter.
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, "for %%d in (")
	assert.Contains(t, out, AgentVolumeMarker)
}

func TestGenerateWinPEAgent_AcceptsVolumeAsArgument(t *testing.T) {
	// The answer-volume launcher already knows the letter it found the agent
	// on; passing it skips the marker search (which stays as the fallback for
	// the future boot.wim path, where the agent starts before any search).
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, `"%1"`, "must accept the volume as its first argument")
}

func TestGenerateWinPEAgent_SnapshotsSetupLogsEveryPoll(t *testing.T) {
	// WinPE keeps Setup's logs on the X: RAM disk, which dies with the VM —
	// the exact forensics gap when an install fails mid-windowsPE (CELL-364).
	// Every poll, the agent copies them to the answer volume, where the host
	// reads them live or post-mortem.
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, `X:\Windows\Panther\setupact.log`)
	assert.Contains(t, out, `X:\Windows\Panther\setuperr.log`)
	assert.Contains(t, out, SetupActSnapshotName)
	assert.Contains(t, out, SetupErrSnapshotName)

	snapIdx := strings.Index(out, SetupActSnapshotName)
	loopIdx := strings.Index(out, ":loop")
	assert.Greater(t, snapIdx, loopIdx, "snapshots happen inside the poll loop, not once")
}

func TestGenerateWinPEAgentLauncher_CannotFailAndFindsTheAgent(t *testing.T) {
	// This string runs as a windowsPE RunSynchronous command, where any
	// non-zero exit ABORTS Setup (0x80070001 - 0x40030). It probes letters
	// with `if exist` (which cannot fail), starts the agent detached (so
	// Setup is never blocked), and force-exits 0.
	cmd := WinPEAgentLauncherCommand()
	assert.True(t, strings.HasPrefix(cmd, "cmd /c "), "must run under cmd")
	assert.Contains(t, cmd, "if exist")
	assert.Contains(t, cmd, AgentScriptName)
	assert.Contains(t, cmd, "start ")
	assert.True(t, strings.HasSuffix(cmd, "exit /b 0"), "must force exit 0: %s", cmd)
}
