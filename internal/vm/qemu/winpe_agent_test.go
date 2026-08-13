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

	assert.Contains(t, out, "cmd.exe, /c "+WinPEBootstrapPath,
		"winpeshl.exe uses CreateProcess — .cmd files need cmd.exe /c prefix")
	assert.NotContains(t, out, "\n[", "must use CRLF line endings for Windows INI parser")
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
	// The guest writes progress to a virtio-serial port (CELL-430); the host
	// reads it as text from GuestProgressLogPath.
	port := `\\.\Global\` + ProgressPortName
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{ProgressPort: port}))
	assert.Contains(t, out, ">"+port)
	assert.Contains(t, out, "devcell:")
}

func TestGenerateWinPEBootstrap_StartsAgentDetached(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	// winpeshl runs entries synchronously — the agent must not block setup.exe
	assert.Contains(t, out, "start ")
	assert.Contains(t, out, WinPEAgentPath)
}

func TestGenerateWinPEBootstrap_SyncAgentBlocksBootstrap(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{SyncAgent: true}))
	assert.Contains(t, out, "call "+WinPEAgentPath,
		"SyncAgent must use 'call' so bootstrap blocks on the agent")
	assert.NotContains(t, out, "start ",
		"SyncAgent must not detach the agent")
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
	// setup.exe switches its logging to X:\$windows.~bt\Sources\Panther once
	// it takes over — run 20260812T144140 snapshotted nothing because only
	// the early path was copied. The later path is copied second so it wins
	// when both exist.
	assert.Contains(t, out, `X:\$windows.~bt\Sources\Panther\setupact.log`)
	assert.Contains(t, out, `X:\$windows.~bt\Sources\Panther\setuperr.log`)
	assert.Contains(t, out, SetupActSnapshotName)
	assert.Contains(t, out, SetupErrSnapshotName)

	snapIdx := strings.Index(out, SetupActSnapshotName)
	loopIdx := strings.Index(out, ":loop")
	assert.Greater(t, snapIdx, loopIdx, "snapshots happen inside the poll loop, not once")
}

func TestGenerateWinPEHyperVDiagScript_StructuredOutput(t *testing.T) {
	progPort := `\\.\Global\` + ProgressPortName
	out := string(GenerateWinPEHyperVDiagScript(progPort))

	assert.Contains(t, out, "DEVCELL HYPERV DIAGNOSTICS", "must have a recognisable header")
	assert.Contains(t, out, "DEVCELL HYPERV DIAGNOSTICS COMPLETE", "must have a completion marker")

	// System info
	assert.Contains(t, out, "SYSTEM INFO", "must report system info")
	assert.Contains(t, out, "PROCESSOR_ARCHITECTURE", "must report CPU architecture")

	// BCD
	assert.Contains(t, out, "bcdedit", "must query BCD for hypervisor launch config")
	assert.Contains(t, out, "hypervisorsettings", "must query BCD hypervisor settings")
	assert.Contains(t, out, "bcdedit /enum ALL", "must dump full BCD store")

	// Binaries
	assert.Contains(t, out, "hvaa64.exe", "must verify hypervisor binary is present")
	assert.Contains(t, out, "hvloader.dll", "must verify hypervisor loader is present")
	assert.Contains(t, out, "hvservice.sys", "must verify hypervisor service driver is present")
	assert.Contains(t, out, "winhv.sys", "must verify WinHV platform driver is present")
	assert.Contains(t, out, "vmms.exe", "must check for vmms binary")

	// Driver registry details
	assert.Contains(t, out, "DRIVER REGISTRY DETAILS", "must dump full driver registry keys")

	// DISM
	assert.Contains(t, out, "dism", "must query DISM for installed packages")
	assert.Contains(t, out, "Get-Features", "must query DISM features")
	assert.Contains(t, out, "Hyper-V", "must reference Hyper-V")

	// Service state
	assert.Contains(t, out, "HYPERV SERVICE STATE", "must report Hyper-V service state")
	assert.Contains(t, out, "WSL SERVICE STATE", "must report WSL2 service state")
	assert.Contains(t, out, "vmms", "must check the Hyper-V VMMS service")
	assert.Contains(t, out, "DependOnService", "must query driver dependencies")

	// Hypervisor detection
	assert.Contains(t, out, "HYPERVISOR DETECTION", "must probe for hypervisor presence")
	assert.Contains(t, out, "DeviceGuard", "must check VBS/Device Guard state")
	assert.Contains(t, out, "CentralProcessor", "must dump processor info from registry")

	// Start services
	assert.Contains(t, out, "START HYPERV SERVICES", "must attempt to start services")

	// Event logs
	assert.Contains(t, out, "EVENT LOGS", "must collect event logs")
	assert.Contains(t, out, "Hyper-V-Hypervisor-Operational", "must check hypervisor operational log")

	// SetupAPI
	assert.Contains(t, out, "SETUPAPI LOGS", "must check driver setup logs")
	assert.Contains(t, out, "setupapi.dev.log", "must dump setupapi device log")

	// Final status
	assert.Contains(t, out, "FINAL DRIVER STATUS", "must report final driver status")
	assert.Contains(t, out, "POST-MORTEM SUMMARY", "must include post-mortem summary")
	assert.Contains(t, out, "net start 2>&1", "must list all running services")

	// Progress markers
	assert.Contains(t, out, ">"+progPort, "must echo progress to virtio-serial port for live monitoring")
	assert.Contains(t, out, "hyperv-diag-start", "must report start to serial")
	assert.Contains(t, out, "hyperv-diag-complete", "must report completion to serial")

	noSerial := string(GenerateWinPEHyperVDiagScript(""))
	assert.NotContains(t, noSerial, "devcell:", "no serial output when progressPort is empty")
}

func TestWinPEHyperVDiagScriptCommand_InvokesScript(t *testing.T) {
	cmd := WinPEHyperVDiagScriptCommand()
	assert.Contains(t, cmd, WinPEHyperVDiagScriptName, "must reference the script name")
	assert.Contains(t, cmd, "%DEVCELL_VOL%", "must use percent-expansion volume ref")
}

func TestGenerateWinPEShellINI_NoSetup_RunsOnlyBootstrap(t *testing.T) {
	out := string(GenerateWinPEShellINI_NoSetup())
	assert.Contains(t, out, "[LaunchApps]")
	assert.Contains(t, out, WinPEBootstrapPath, "must launch bootstrap")
	assert.NotContains(t, out, "setup.exe", "must NOT launch setup.exe")
	assert.Contains(t, out, "cmd.exe, /c "+WinPEBootstrapPath,
		"winpeshl.exe uses CreateProcess — .cmd files need cmd.exe /c prefix")
}

func TestGenerateWinPEBootstrap_WPEInit(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{WPEInit: true, ProgressPort: `\\.\Global\` + ProgressPortName}))
	wpeinitIdx := strings.Index(out, "wpeinit")
	bootstrapIdx := strings.Index(out, "devcell:")
	assert.Positive(t, wpeinitIdx, "must call wpeinit")
	assert.Less(t, wpeinitIdx, bootstrapIdx, "wpeinit must run before any progress output")
}

func TestGenerateWinPEBootstrap_NoWPEInitByDefault(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	assert.NotContains(t, out, "wpeinit", "default bootstrap must not call wpeinit")
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

func TestGenerateWinPEEchoProbeScript_ProbesCOM1Through4(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("devcell-logs"))
	assert.Contains(t, out, "COM PORT PROBE")
	for i := 1; i <= 4; i++ {
		marker := "DEVCELL_COM_ECHO_COM" + string(rune('0'+i))
		assert.Contains(t, out, marker, "must echo marker for COM%d", i)
	}
	assert.Contains(t, out, "COM PROBE DONE")
}

func TestGenerateWinPEEchoProbeScript_LoadsViofsDriver(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("devcell-logs"))
	assert.Contains(t, out, "drvload")
	assert.Contains(t, out, "viofs.inf")
}

func TestGenerateWinPEEchoProbeScript_MountsVirtiofs(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("my-tag"))
	assert.Contains(t, out, "virtiofs.exe mount -t my-tag V:")
	assert.Contains(t, out, "DEVCELL_VIOFS_HELLO")
	assert.Contains(t, out, "viofs-probe.txt")
}

func TestGenerateWinPEEchoProbeScript_RunsToCompletion(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("devcell-logs"))
	assert.Contains(t, out, "DEVCELL ECHO PROBE COMPLETE")
}

func TestWinPEEchoProbeScriptCommand_InvokesOnAnswerVolume(t *testing.T) {
	cmd := WinPEEchoProbeScriptCommand()
	assert.Contains(t, cmd, WinPEEchoProbeScriptName)
	assert.Contains(t, cmd, "%DEVCELL_VOL%")
}
