package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateWinPEShellINI_RunsBootstrapBeforeSetup(t *testing.T) {
	out := string(GenerateWinPEShellINI())
	assert.Contains(t, out, "[LaunchApps]")

	bootstrapIdx := strings.Index(out, WinPEBootstrapCmdPath)
	setupIdx := strings.Index(out, "setup.exe")
	assert.Positive(t, bootstrapIdx, "bootstrap.cmd must be listed")
	assert.Positive(t, setupIdx, "setup.exe must still run")
	assert.Less(t, bootstrapIdx, setupIdx, "bootstrap must run before setup.exe")

	assert.Contains(t, out, WinPEBootstrapCmdPath,
		"winpeshl.ini must call the cmd.exe shim (stock WinPE lacks powershell.exe)")
	assert.NotContains(t, out, "powershell.exe",
		"must not reference powershell.exe (not present in stock WinPE)")
	assert.NotContains(t, out, "\n[", "must use CRLF line endings for Windows INI parser")
}

func TestGenerateWinPEBootstrap_NoDriversByDefault(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	assert.NotContains(t, out, "drvload")
}

func TestGenerateWinPEBootstrap_LoadsRequestedDrivers(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{
		DriverINFs: []string{`X:\devcell\drivers\viostor.inf`, `X:\devcell\drivers\netkvm.inf`},
	}))
	assert.Contains(t, out, `drvload.exe 'X:\devcell\drivers\viostor.inf'`)
	assert.Contains(t, out, `drvload.exe 'X:\devcell\drivers\netkvm.inf'`)
}

func TestGenerateWinPEBootstrap_ReportsProgressToSerial(t *testing.T) {
	port := `\\.\Global\` + ProgressPortName
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{ProgressPort: port}))
	assert.Contains(t, out, port, "must reference the progress port")
	assert.Contains(t, out, "devcell:", "must emit devcell progress markers")
	assert.Contains(t, out, "Out-File", "must use Out-File for progress output")
}

func TestGenerateWinPEBootstrap_StartsAgentDetached(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{}))
	assert.Contains(t, out, "Start-Process", "must use Start-Process to detach the agent")
	assert.Contains(t, out, WinPEAgentPath)
	assert.Contains(t, out, `$PSHOME\pwsh.exe`,
		"must use $PSHOME to locate the same pwsh.exe binary")
}

func TestGenerateWinPEBootstrap_SyncAgentBlocksBootstrap(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{SyncAgent: true}))
	assert.Contains(t, out, WinPEAgentPath,
		"SyncAgent must invoke the agent script")
	assert.NotContains(t, out, "Start-Process",
		"SyncAgent must not detach the agent")
	assert.Contains(t, out, `$PSHOME\pwsh.exe`,
		"must use $PSHOME to locate the same pwsh.exe binary")
}

func TestGenerateWinPEAgent_PollsCommandFileAndWritesResult(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, AgentCommandFile, "must poll the command file")
	assert.Contains(t, out, AgentResultFile, "must write results back")
	assert.Contains(t, out, "Remove-Item", "must consume the command so it runs once")
}

// A command that dies with a terminating error used to land only in the
// result file, which the host cannot read until QEMU exits. The run then
// looked like a hang and burned its whole deadline before revealing a
// one-line error.
func TestGenerateWinPEAgent_StreamsCaughtErrorsToProgress(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{ProgressPort: `\\.\Global\devcell`}))

	catchIdx := strings.LastIndex(out, "} catch {")
	require.Greater(t, catchIdx, 0, "agent must catch command failures")

	setContent := strings.Index(out[catchIdx:], "Set-Content")
	progress := strings.Index(out[catchIdx:], "devcell-progress")
	require.Greater(t, setContent, 0, "the catch block still writes the result file")
	assert.Less(t, progress, setContent,
		"a caught error must reach the progress stream before the result file, "+
			"which the host cannot read until QEMU exits")
}

// drvload.exe has no verbose switch, so its exit code is the only direct
// signal, and it was being discarded. 0x80070103 (ERROR_NO_MORE_ITEMS) means
// the driver was already bound, which reads identically to a real failure on
// a console screenshot.
func TestGenerateWinPEBootstrap_ReportsDrvLoadExitCodes(t *testing.T) {
	out := string(GenerateWinPEBootstrap(WinPEPayloadConfig{
		ProgressPort: `\\.\Global\devcell`,
		DriverINFs:   []string{`X:\devcell\drivers\vioserial\vioser.inf`},
	}))

	assert.Contains(t, out, "$LASTEXITCODE",
		"drvload's exit code must be captured, not discarded")
	assert.Contains(t, out, "drvload",
		"the report must name the operation")

	// Reporting has to come after the loop: the virtio-serial port device
	// does not exist until its own driver loads.
	loadIdx := strings.Index(out, "drvload.exe")
	reportIdx := strings.LastIndex(out, "drvload")
	assert.Greater(t, reportIdx, loadIdx,
		"exit codes are reported after the drivers load, not before")
}

// PnP writes a full driver-binding trace to setupapi.dev.log. Without it the
// only evidence of what drvload did is a one-line hex code on screen.
func TestGenerateWinPEAgent_SnapshotsSetupAPILog(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))

	assert.Contains(t, out, `X:\Windows\INF\setupapi.dev.log`,
		"the PnP driver log must be pulled off the ramdisk")
	assert.Contains(t, out, SetupAPISnapshotName)
}

func TestGenerateWinPEAgent_SearchesForTheCommandVolume(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, "foreach", "must iterate drive letters")
	assert.Contains(t, out, AgentVolumeMarker)
}

func TestGenerateWinPEAgent_AcceptsVolumeAsArgument(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, "$args", "must accept the volume as an argument")
}

func TestGenerateWinPEAgent_SnapshotsSetupLogsEveryPoll(t *testing.T) {
	out := string(GenerateWinPEAgent(WinPEPayloadConfig{}))
	assert.Contains(t, out, `X:\Windows\Panther\setupact.log`)
	assert.Contains(t, out, `X:\Windows\Panther\setuperr.log`)
	assert.Contains(t, out, `X:\$windows.~bt\Sources\Panther\setupact.log`)
	assert.Contains(t, out, `X:\$windows.~bt\Sources\Panther\setuperr.log`)
	assert.Contains(t, out, SetupActSnapshotName)
	assert.Contains(t, out, SetupErrSnapshotName)

	snapIdx := strings.Index(out, SetupActSnapshotName)
	loopIdx := strings.Index(out, "while ($true)")
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
	assert.Contains(t, out, "BINARIES_TOTAL_MISSING=", "must emit parseable binary count")

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
	assert.Contains(t, out, "_NET_START_EXIT=", "must emit per-service net start exit code")
	assert.Contains(t, out, "sc.exe", "must query service state after start attempts")
	assert.Contains(t, out, "_SC_EXIT=", "must emit per-service sc query exit code")
	assert.Contains(t, out, "_SC_STATE=", "must emit per-service STATE from sc query")
	assert.Contains(t, out, "tasklist.exe", "must list service-hosting processes")

	// Event logs
	assert.Contains(t, out, "EVENT LOGS", "must collect event logs")
	assert.Contains(t, out, "Hyper-V-Hypervisor-Operational", "must check hypervisor operational log")

	// SetupAPI
	assert.Contains(t, out, "SETUPAPI LOGS", "must check driver setup logs")
	assert.Contains(t, out, "setupapi.dev.log", "must dump setupapi device log")
	assert.Contains(t, out, "SETUPAPI_ERRORS=", "must emit parseable setupapi error summary")

	// Final status
	assert.Contains(t, out, "FINAL DRIVER STATUS", "must report final driver status")
	assert.Contains(t, out, "_START_VALUE=", "must emit parseable Start value per registered service")
	assert.Contains(t, out, "POST-MORTEM SUMMARY", "must include post-mortem summary")
	assert.Contains(t, out, "net.exe start", "must list all running services")

	// Progress markers
	assert.Contains(t, out, progPort, "must reference the progress port for live monitoring")
	assert.Contains(t, out, "hyperv-diag-start", "must report start to serial")
	assert.Contains(t, out, "hyperv-diag-complete", "must report completion to serial")

	noSerial := string(GenerateWinPEHyperVDiagScript(""))
	assert.NotContains(t, noSerial, "devcell:", "no serial output when progressPort is empty")
}

func TestWinPEHyperVDiagScriptCommand_InvokesScript(t *testing.T) {
	cmd := WinPEHyperVDiagScriptCommand()
	assert.Contains(t, cmd, WinPEHyperVDiagScriptName, "must reference the script name")
	assert.Contains(t, cmd, "$DevcellVol", "must use PowerShell variable for volume ref")
}

func TestGenerateWinPEShellINI_NoSetup_RunsOnlyBootstrap(t *testing.T) {
	out := string(GenerateWinPEShellINI_NoSetup())
	assert.Contains(t, out, "[LaunchApps]")
	assert.Contains(t, out, WinPEBootstrapCmdPath, "must launch bootstrap.cmd")
	assert.NotContains(t, out, "setup.exe", "must NOT launch setup.exe")
	assert.NotContains(t, out, "powershell.exe",
		"must not reference powershell.exe (not present in stock WinPE)")
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
	assert.NotContains(t, out, "wpeinit")
}

func TestGenerateWinPEAgentLauncher_CannotFailAndFindsTheAgent(t *testing.T) {
	cmd := WinPEAgentLauncherCommand()
	assert.True(t, strings.HasPrefix(cmd, "cmd.exe"),
		"must use cmd.exe (stock WinPE lacks powershell.exe)")
	assert.Contains(t, cmd, AgentScriptName)
	assert.Contains(t, cmd, PwshVolDir+`\pwsh.exe`,
		"must locate pwsh.exe on the answer volume")
	assert.Contains(t, cmd, "start /min",
		"must start agent detached so Setup is never blocked")
	assert.True(t, strings.HasSuffix(cmd, `exit /b 0"`),
		"must force exit /b 0: %s", cmd)
}

func TestGenerateWinPEBootstrapCmd_ProbesForPwsh(t *testing.T) {
	out := string(GenerateWinPEBootstrapCmd())
	assert.Contains(t, out, PwshVolDir+`\pwsh.exe`,
		"must probe for pwsh.exe on volumes")
	assert.Contains(t, out, WinPEBootstrapPath,
		"must launch the PowerShell bootstrap")
	assert.Contains(t, out, "%%d",
		"must use %%d (batch for-loop variable)")
	assert.Contains(t, out, "goto :eof",
		"must stop after first match")
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
	assert.Contains(t, out, "drvload.exe")
	assert.Contains(t, out, "viofs.inf")
}

func TestGenerateWinPEEchoProbeScript_MountsVirtiofs(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("my-tag"))
	assert.Contains(t, out, `virtiofs.exe" mount -t my-tag V:`)
	assert.Contains(t, out, "DEVCELL_VIOFS_HELLO")
	assert.Contains(t, out, "viofs-probe.txt")
}

func TestGenerateWinPEEchoProbeScript_RunsToCompletion(t *testing.T) {
	out := string(GenerateWinPEEchoProbeScript("devcell-logs"))
	assert.Contains(t, out, "DEVCELL ECHO PROBE COMPLETE")
}

func TestWinPEDiagToolPaths_ContainsCoreTools(t *testing.T) {
	paths := WinPEDiagToolPaths()
	require.NotEmpty(t, paths, "must inject at least one tool")
	for _, p := range paths {
		assert.True(t, strings.HasPrefix(p, `\Windows\System32\`),
			"tool %s must be a System32 binary", p)
		assert.True(t, strings.HasSuffix(p, ".exe"),
			"tool %s must be an executable", p)
	}
	joined := strings.Join(paths, " ")
	assert.Contains(t, joined, "sc.exe", "sc.exe is required for service state queries")
	assert.Contains(t, joined, "wevtutil.exe", "wevtutil.exe is required for event log queries")
}

func TestWinPEEchoProbeScriptCommand_InvokesOnAnswerVolume(t *testing.T) {
	cmd := WinPEEchoProbeScriptCommand()
	assert.Contains(t, cmd, WinPEEchoProbeScriptName)
	assert.Contains(t, cmd, "$DevcellVol")
}

func TestGeneratedPS1_SyntaxValid(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not on PATH")
	}

	progressPort := `\\.\Global\` + ProgressPortName
	cfg := WinPEPayloadConfig{
		WPEInit:      true,
		ProgressPort: progressPort,
		PollSeconds:  5,
		SyncAgent:    true,
		DriverINFs: []string{
			`X:\devcell\drivers\vioserial\vioser.inf`,
			`X:\devcell\drivers\vioscsi\vioscsi.inf`,
		},
	}

	scripts := map[string][]byte{
		"bootstrap.ps1":         GenerateWinPEBootstrap(cfg),
		"agent.ps1":             GenerateWinPEAgent(cfg),
		"winpe-diag.ps1":        GenerateWinPEDiagScript(),
		"hyperv-diag.ps1":       GenerateWinPEHyperVDiagScript(progressPort),
		"echo-probe.ps1":        GenerateWinPEEchoProbeScript("devcell-viofs"),
		"guest-diagnostics.ps1": GenerateGuestDiagnosticsScript(),
	}

	outDir := t.TempDir()
	for name, data := range scripts {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(outDir, name)
			require.NoError(t, os.WriteFile(p, data, 0644))

			out, err := exec.Command(pwsh, "-NoProfile", "-Command",
				"$errors = $null; "+
					"[System.Management.Automation.Language.Parser]::ParseFile('"+p+"', [ref]$null, [ref]$errors) | Out-Null; "+
					"if ($errors.Count -gt 0) { foreach ($e in $errors) { Write-Error \"$($e.Message) at line $($e.Extent.StartLineNumber)\" }; exit 1 }",
			).CombinedOutput()
			if err != nil {
				t.Fatalf("syntax errors:\n%s", out)
			}
		})
	}
}
