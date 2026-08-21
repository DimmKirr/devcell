package qemu

import (
	"fmt"
	"strings"
)

// WinPE payload layout. These files are baked into boot.wim so they exist on
// the WinPE RAM drive (X:) before setup.exe starts.
const (
	// WinPEPayloadDir is where the devcell payload lives inside boot.wim.
	WinPEPayloadDir = `X:\devcell`
	// WinPEBootstrapCmdPath is the cmd.exe shim that winpeshl.ini calls.
	// Stock WinPE lacks powershell.exe; this shim probes volumes for pwsh.exe
	// (PowerShell 7, xcopy-deployed on the answer volume) and launches the
	// real bootstrap.ps1 through it.
	WinPEBootstrapCmdPath = `X:\devcell\bootstrap.cmd`
	// WinPEBootstrapPath is the PowerShell bootstrap, launched by the cmd shim.
	WinPEBootstrapPath = `X:\devcell\bootstrap.ps1`
	// WinPEAgentPath is the control agent, started detached by the bootstrap.
	WinPEAgentPath = `X:\devcell\agent.ps1`

	// PwshVolDir is the directory on the answer volume containing PowerShell 7.
	// Stock WinPE has no PowerShell; pwsh.exe is self-contained and
	// xcopy-deployed from the official GitHub release zip.
	PwshVolDir = `pwsh`

	// AgentVolumeMarker identifies the removable volume carrying the command
	// and result files. WinPE drive letters are not stable, so the agent
	// searches for this file instead of assuming a letter.
	AgentVolumeMarker = `devcell-agent.marker`
	// AgentCommandFile holds a single command line for the agent to run.
	AgentCommandFile = `devcell-cmd.txt`
	// AgentResultFile receives that command's combined output.
	AgentResultFile = `devcell-out.txt`
	// AgentDoneFile is written after the command finishes. The host polls for
	// this instead of AgentResultFile to avoid reading a half-written output
	// file (the redirect flushes incrementally, so the file appears non-empty
	// before diskpart/PowerShell finishes writing).
	AgentDoneFile = `devcell-done.marker`

	// AgentScriptName is the agent's filename on the answer volume — the
	// no-rebake deployment path: a windowsPE RunSynchronous launcher starts
	// it straight off the volume (WinPEAgentLauncherCommand), so boot.wim
	// never has to be modified.
	AgentScriptName = `devcell-agent.ps1`
	// SetupActSnapshotName receives the agent's periodic copy of WinPE's
	// X:\Windows\Panther\setupact.log, which otherwise dies with the RAM
	// disk (CELL-364).
	SetupActSnapshotName = `devcell-setupact.log`
	// SetupErrSnapshotName receives setuperr.log the same way.
	SetupErrSnapshotName = `devcell-setuperr.log`

	// ProgressPortName lives in command.go (where the QEMU device is wired).
)

// WinPEAgentLauncherCommand returns the one non-registry command allowed in
// windowsPE RunSynchronous. Anything that can fail there aborts Setup
// (0x80070001 - 0x40030, run 20260729T172019), so the whole block is
// wrapped in exit /b 0 to guarantee a zero exit code. The agent is started
// detached via "start /min" so Setup is never blocked.
//
// Uses cmd.exe because stock WinPE lacks powershell.exe. The answer volume
// carries pwsh.exe (PowerShell 7) which the agent needs at runtime.
func WinPEAgentLauncherCommand() string {
	return `cmd.exe /c "for %l in (C D E F G H I J K L) do @if exist %l:\` + PwshVolDir + `\pwsh.exe if exist %l:\` + AgentScriptName + ` start /min %l:\` + PwshVolDir + `\pwsh.exe -ExecutionPolicy Bypass -File %l:\` + AgentScriptName + ` %l: & exit /b 0"`
}

// WinPEDriverLoadCommand returns a windowsPE RunSynchronous command that
// drvloads one INF from whatever drive letter the answer volume received.
//
// This is the last hook before Modern Setup searches for install media:
// run 20260812T150644 logged "WinPEInitialization: Leaving Execute Method"
// and "EarlyF6DriverInstall: Entering Execute Method" one second apart, in
// that order. The agent's poll loop is too late: its drvload landed after
// the media search had already failed (0x80070103, run 20260812T143146).
//
// Uses cmd.exe because stock WinPE lacks powershell.exe. drvload.exe is a
// WinPE native tool. Wrapped in exit /b 0 so a broken driver degrades
// gracefully instead of aborting Setup.
func WinPEDriverLoadCommand(inf string) string {
	return `cmd.exe /c "for %l in (C D E F G H I J K L) do @if exist %l:\` + inf + ` drvload.exe %l:\` + inf + `" & exit /b 0`
}

// WinPEDiagCommand is the one-shot diagnostic the agent executes when a
// build ships it as AgentCommand; its combined output lands in
// devcell-out.txt on the answer volume. Strictly read-only: the first
// version drvloaded vioscsi and collided with wpeinit's own $WinPEDriver$
// load — Setup aborted 0x80070103 ERROR_NO_MORE_ITEMS, run 20260812T143146.
//
// Deprecated: prefer WinPEDiagScriptCommand, which invokes the proper
// diagnostics script and waits for completion before the output is read.
const WinPEDiagCommand = `Set-Content X:\devcell-lv.txt "list volume`+"`r`n"+`exit"; & diskpart.exe /s X:\devcell-lv.txt; & reg.exe query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi; Get-ChildItem X:\Windows\Panther, X:\$windows.~bt\Sources\Panther -ErrorAction SilentlyContinue`

const (
	// WinPEDiagScriptName is the diagnostics script shipped on the answer
	// volume. It follows the same structured-output pattern as
	// GenerateGuestDiagnosticsScript (guest_diagnostics.go) but runs in
	// WinPE under PowerShell.
	WinPEDiagScriptName = `devcell-winpe-diag.ps1`
)

// WinPEDiagScriptCommand returns the agent command that invokes the
// diagnostics script. The agent runs this via Invoke-Expression in
// PowerShell, so $DevcellVol is expanded from the agent's scope.
func WinPEDiagScriptCommand() string {
	return `& "$DevcellVol\` + WinPEDiagScriptName + `" $DevcellVol`
}

// GenerateWinPEDiagScript produces the WinPE diagnostics script. It is
// shipped on the answer volume and invoked by the agent. Output goes to
// stdout (the agent redirects it to AgentResultFile).
//
// Three sections:
//  1. Disk/volume enumeration
//  2. CIM/PowerShell probes
//  3. Script access: can we see other devcell scripts on the answer volume
func GenerateWinPEDiagScript() []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Vol = $args[0]\r\n")
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DEVCELL WINPE DIAGNOSTICS ==='\r\n")
	b.WriteString("Write-Output \"$(Get-Date)\"\r\n")
	b.WriteString("Write-Output \"Volume: $Vol\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 0. CPU / PROCESSOR CAPABILITIES
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== PROCESSOR INFO ==='\r\n")
	b.WriteString("Write-Output \"PROCESSOR_ARCHITECTURE=$env:PROCESSOR_ARCHITECTURE\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_IDENTIFIER=$env:PROCESSOR_IDENTIFIER\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_LEVEL=$env:PROCESSOR_LEVEL\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_REVISION=$env:PROCESSOR_REVISION\"\r\n")
	b.WriteString("Write-Output \"NUMBER_OF_PROCESSORS=$env:NUMBER_OF_PROCESSORS\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== CPU REGISTRY ==='\r\n")
	b.WriteString("& reg.exe query 'HKLM\\HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0' 2>$null\r\n")
	b.WriteString("Write-Output ''\r\n")

	for _, item := range []struct{ section, wmic string }{
		{"WMIC CPU (full)", "cpu"},
		{"WMIC COMPUTERSYSTEM", "computersystem"},
		{"WMIC BASEBOARD", "baseboard"},
		{"WMIC BIOS", "bios"},
		{"WMIC MEMORYCHIP", "memorychip"},
		{"WMIC OS", "os"},
	} {
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "Write-Output '=== %s ==='\r\n", item.section)
		fmt.Fprintf(&b, "try { & wmic.exe %s get /format:list 2>$null } catch { Write-Output 'wmic %s: not available' }\r\n", item.wmic, item.wmic)
		b.WriteString("Write-Output ''\r\n")
	}

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== SYSTEMINFO ==='\r\n")
	b.WriteString("try { & systeminfo.exe 2>$null } catch { Write-Output 'systeminfo: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 1. DISK CHECKS
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DISKPART VOLUMES ==='\r\n")
	b.WriteString("Set-Content X:\\devcell-lv.txt \"list volume`r`nexit\"\r\n")
	b.WriteString("& diskpart.exe /s X:\\devcell-lv.txt\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DISKPART DISKS ==='\r\n")
	b.WriteString("Set-Content X:\\devcell-ld.txt \"list disk`r`nexit\"\r\n")
	b.WriteString("& diskpart.exe /s X:\\devcell-ld.txt\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== WMIC LOGICALDISK ==='\r\n")
	b.WriteString("try { & wmic.exe logicaldisk get caption,description,filesystem,volumename,size 2>$null } catch { Write-Output 'wmic: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== STORAGE DRIVERS ==='\r\n")
	for _, drv := range []string{"USBSTOR", "vioscsi", "viostor", "storahci"} {
		fmt.Fprintf(&b, "Write-Output '-- %s:'\r\n", drv)
		fmt.Fprintf(&b, "& reg.exe query 'HKLM\\SYSTEM\\CurrentControlSet\\Services\\%s' /v Start 2>$null\r\n", drv)
		fmt.Fprintf(&b, "if ($LASTEXITCODE -ne 0) { Write-Output '  not loaded' }\r\n")
	}
	b.WriteString("Write-Output ''\r\n")

	// ── 2. POWERSHELL PROBES (already running in PS, so just call directly)
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== POWERSHELL AVAILABILITY ==='\r\n")
	b.WriteString("Write-Output 'powershell.exe: found'\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL VERSION ==='\r\n")
	b.WriteString("$PSVersionTable | Format-List\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL ADMIN CHECK ==='\r\n")
	b.WriteString("Write-Output ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL GET-VOLUME ==='\r\n")
	b.WriteString("try { Get-Volume | Format-Table DriveLetter, FileSystemLabel, DriveType, FileSystem, @{N='SizeGB';E={[math]::Round($_.Size/1GB,1)}} -AutoSize | Out-String -Width 200 } catch { Write-Output 'Get-Volume: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL GET-DISK ==='\r\n")
	b.WriteString("try { Get-Disk | Format-Table Number, FriendlyName, BusType, Size, PartitionStyle -AutoSize | Out-String -Width 200 } catch { Write-Output 'Get-Disk: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL CPU (full) ==='\r\n")
	b.WriteString("try { Get-CimInstance Win32_Processor | Format-List * } catch { Write-Output 'Get-CimInstance: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL COMPUTERSYSTEM ==='\r\n")
	b.WriteString("try { Get-CimInstance Win32_ComputerSystem | Format-List * } catch { Write-Output 'Get-CimInstance: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 3. SCRIPT ACCESS
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== ANSWER VOLUME CONTENTS ==='\r\n")
	b.WriteString("if (-not $Vol) {\r\n")
	b.WriteString("    Write-Output 'VOL not set, skipping'\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Get-ChildItem \"$Vol\\\" -ErrorAction SilentlyContinue\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '=== DEVCELL SCRIPTS ==='\r\n")
	fmt.Fprintf(&b, "    foreach ($f in @('%s','%s','%s','autounattend.xml')) {\r\n",
		AgentScriptName, AgentVolumeMarker, BootstrapScriptName)
	b.WriteString("        if (Test-Path \"$Vol\\$f\") {\r\n")
	b.WriteString("            Write-Output \"[OK]    $Vol\\$f\"\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output \"[MISS]  $Vol\\$f\"\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("}\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== PANTHER LOGS ==='\r\n")
	b.WriteString("Get-ChildItem X:\\Windows\\Panther -ErrorAction SilentlyContinue\r\n")
	b.WriteString("Write-Output '---'\r\n")
	b.WriteString("Get-ChildItem 'X:\\$windows.~bt\\Sources\\Panther' -ErrorAction SilentlyContinue\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DEVCELL DIAGNOSTICS COMPLETE ==='\r\n")

	return []byte(b.String())
}

const (
	// WinPEHyperVDiagScriptName is the diagnostics script that probes
	// Hyper-V and WSL2 feature/service state inside WinPE. It mounts
	// install.wim from the attached Windows ISO, uses DISM to enable
	// features offline, loads the offline registry to enable services,
	// then queries their state after reloading.
	WinPEHyperVDiagScriptName = `devcell-winpe-hyperv-diag.ps1`

	// WinPEEchoProbeScriptName is the filename for the COM-port echo
	// probe + virtiofs write test script.
	WinPEEchoProbeScriptName = `devcell-winpe-echo-probe.ps1`
)

// WinPEDiagToolPaths returns the WIM-internal paths of System32 binaries
// to extract from install.wim and inject into boot.wim. Stock WinPE
// lacks these; injecting them gives the diagnostics script real service
// management and process visibility.
func WinPEDiagToolPaths() []string {
	return []string{
		`\Windows\System32\sc.exe`,
		`\Windows\System32\tasklist.exe`,
		`\Windows\System32\wevtutil.exe`,
	}
}

// WinPEHyperVDiagScriptCommand returns the agent command that invokes the
// Hyper-V/WSL2 diagnostics script.
func WinPEHyperVDiagScriptCommand() string {
	return `& "$DevcellVol\` + WinPEHyperVDiagScriptName + `" $DevcellVol`
}

// GenerateWinPEHyperVDiagScript produces a WinPE script that verifies
// the Hyper-V hypervisor host stack is present and configured in boot.wim.
//
// boot.wim ships hvaa64.exe (the hypervisor), hvloader.dll, hvservice.sys,
// winhv.sys, winhvr.sys, and hvhostsvc.dll. The stock BCD already sets
// hypervisorlaunchtype=Auto. This script confirms:
//  1. BCD hypervisor configuration (bcdedit)
//  2. Hypervisor host binaries exist on the WinPE RAM disk
//  3. Hypervisor driver/service state (loaded? running?)
//  4. DISM online packages containing Hyper-V
//  5. Offline registry service entries
//
// When progressPort is non-empty, section headers are echoed to that device
// path so the host can monitor progress live via guest-progress.log.
func GenerateWinPEHyperVDiagScript(progressPort string) []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Vol = $args[0]\r\n")

	serial := func(msg string) {
		if progressPort != "" {
			fmt.Fprintf(&b, "\"devcell: %s\" | Out-File -Append '%s' -Encoding utf8\r\n", msg, progressPort)
		}
	}

	serial("hyperv-diag-start")
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DEVCELL HYPERV DIAGNOSTICS ==='\r\n")
	b.WriteString("Write-Output \"$(Get-Date)\"\r\n")
	b.WriteString("Write-Output \"Volume: $Vol\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 1. SYSTEM / ARCHITECTURE INFO ──
	b.WriteString("\r\n")
	serial("section-sysinfo")
	b.WriteString("Write-Output '=== SYSTEM INFO ==='\r\n")
	b.WriteString("Write-Output \"-- PROCESSOR_ARCHITECTURE: $env:PROCESSOR_ARCHITECTURE\"\r\n")
	b.WriteString("Write-Output \"-- PROCESSOR_IDENTIFIER:   $env:PROCESSOR_IDENTIFIER\"\r\n")
	b.WriteString("Write-Output \"-- NUMBER_OF_PROCESSORS:    $env:NUMBER_OF_PROCESSORS\"\r\n")
	b.WriteString("Write-Output '-- OS version:'\r\n")
	b.WriteString("[System.Environment]::OSVersion.VersionString\r\n")
	b.WriteString("Write-Output '-- systeminfo (if available):'\r\n")
	b.WriteString("try { & systeminfo.exe 2>&1 } catch { Write-Output '  systeminfo: NOT FOUND' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 2. BCD HYPERVISOR CONFIGURATION ──
	b.WriteString("\r\n")
	serial("section-bcd")
	b.WriteString("Write-Output '=== BCD HYPERVISOR CONFIG ==='\r\n")
	b.WriteString("Write-Output '-- bcdedit /enum {current}:'\r\n")
	b.WriteString("& bcdedit.exe /enum '{current}' 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- bcdedit /enum {hypervisorsettings}:'\r\n")
	b.WriteString("& bcdedit.exe /enum '{hypervisorsettings}' 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- bcdedit /enum ALL (full BCD store):'\r\n")
	b.WriteString("& bcdedit.exe /enum ALL 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 3. HYPERVISOR HOST BINARIES ──
	b.WriteString("\r\n")
	serial("section-binaries")
	b.WriteString("Write-Output '=== HYPERVISOR HOST BINARIES ==='\r\n")
	b.WriteString("$BinariesOk = 0\r\n")
	b.WriteString("$BinariesMissing = 0\r\n")
	b.WriteString("\r\n")
	b.WriteString("foreach ($f in @(\r\n")
	b.WriteString("    'X:\\Windows\\System32\\hvaa64.exe',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\hvloader.dll',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\hvhostsvc.dll',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\hvservice.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\winhv.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\winhvr.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\hvsocket.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\vmbus.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\HvSocket.dll',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\bcdedit.exe',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\vmbusr.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\drivers\\vmbkmcl.sys',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\vmms.exe',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\vmwp.exe',\r\n")
	b.WriteString("    'X:\\Windows\\System32\\vmcompute.exe'\r\n")
	b.WriteString(")) {\r\n")
	b.WriteString("    if (Test-Path $f) {\r\n")
	b.WriteString("        Write-Output \"  FOUND: $f\"\r\n")
	b.WriteString("        $BinariesOk++\r\n")
	b.WriteString("    } else {\r\n")
	b.WriteString("        Write-Output \"  MISSING: $f\"\r\n")
	b.WriteString("        $BinariesMissing++\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output \"Binaries found: $BinariesOk  missing: $BinariesMissing\"\r\n")
	b.WriteString("Write-Output \"BINARIES_TOTAL_MISSING=$BinariesMissing\"\r\n")
	serial("binaries-ok=$BinariesOk missing=$BinariesMissing")
	b.WriteString("Write-Output ''\r\n")

	// ── 4. HYPERVISOR DRIVER REGISTRY DETAILS ──
	b.WriteString("\r\n")
	serial("section-driver-registry")
	b.WriteString("Write-Output '=== DRIVER REGISTRY DETAILS ==='\r\n")
	b.WriteString("foreach ($s in @('hvservice','winhv','winhvr','vmbus','hvsocket','vmbusr','vmbkmcl')) {\r\n")
	b.WriteString("    Write-Output \"-- $s full registry:\"\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" 2>&1\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 5. HYPERVISOR DRIVER STATE (registry-based, driverquery hangs under TCG) ──
	b.WriteString("\r\n")
	serial("section-driverquery")
	b.WriteString("Write-Output '=== HYPERVISOR DRIVER STATE ==='\r\n")
	b.WriteString("Write-Output '-- Driver Start values from CurrentControlSet (0=Boot 1=System 2=Auto 3=Manual 4=Disabled):'\r\n")
	b.WriteString("foreach ($d in @('hvservice','winhv','winhvr','vmbus','hvsocket','vmbusr','vmbkmcl')) {\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$d\" /v Start 2>$null\r\n")
	b.WriteString("    if ($LASTEXITCODE -ne 0) { Write-Output \"  ${d}: not registered\" }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 6. DISM ONLINE PACKAGES ──
	b.WriteString("\r\n")
	serial("section-dism")
	b.WriteString("Write-Output '=== DISM ONLINE PACKAGES ==='\r\n")
	b.WriteString("Write-Output '-- All packages:'\r\n")
	b.WriteString("& dism.exe /Online /Get-Packages 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- DISM features (if available):'\r\n")
	b.WriteString("& dism.exe /Online /Get-Features 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 7. OFFLINE SERVICE ENABLEMENT ──
	b.WriteString("\r\n")
	serial("section-offline-registry")
	b.WriteString("Write-Output '=== OFFLINE SERVICE ENABLE ==='\r\n")
	b.WriteString("Write-Output '-- Loading offline SYSTEM hive:'\r\n")
	b.WriteString("& reg.exe load HKLM\\OFFLINE X:\\Windows\\System32\\config\\SYSTEM 2>&1\r\n")
	b.WriteString("if ($LASTEXITCODE -ne 0) {\r\n")
	b.WriteString("    Write-Output 'ERROR: failed to load SYSTEM hive'\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Querying existing hvservice Start value:'\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /v Start 2>&1\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Setting vmms service Start=2 (Auto):'\r\n")
	b.WriteString("    & reg.exe add \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmms\" /v Start /t REG_DWORD /d 2 /f 2>&1\r\n")
	b.WriteString("    Write-Output \"exit code: $LASTEXITCODE\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Setting hvservice Start=0 (Boot):'\r\n")
	b.WriteString("    & reg.exe add \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /v Start /t REG_DWORD /d 0 /f 2>&1\r\n")
	b.WriteString("    Write-Output \"exit code: $LASTEXITCODE\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Setting vmwp Start=3 (Manual):'\r\n")
	b.WriteString("    & reg.exe add \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmwp\" /v Start /t REG_DWORD /d 3 /f 2>&1\r\n")
	b.WriteString("    Write-Output \"exit code: $LASTEXITCODE\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Listing all Hyper-V related services in hive:'\r\n")
	b.WriteString("    foreach ($s in @('hvservice','vmms','vmwp','vmcompute','hvhost','winhv','winhvr','vmbus','hvsocket','vmbusr','vmbkmcl')) {\r\n")
	b.WriteString("        & reg.exe query \"HKLM\\OFFLINE\\ControlSet001\\Services\\$s\" /v Start 2>$null\r\n")
	b.WriteString("        if ($LASTEXITCODE -eq 0) {\r\n")
	b.WriteString("            Write-Output \"  ${s}: present\"\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output \"  ${s}: not in hive\"\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Full offline hvservice key:'\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /s 2>&1\r\n")
	b.WriteString("    Write-Output '-- Full offline vmbus key:'\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmbus\" /s 2>&1\r\n")
	b.WriteString("    Write-Output '-- Full offline winhv key:'\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\OFFLINE\\ControlSet001\\Services\\winhv\" /s 2>&1\r\n")
	b.WriteString("\r\n")
	b.WriteString("    Write-Output '-- Unloading offline hive:'\r\n")
	b.WriteString("    & reg.exe unload HKLM\\OFFLINE 2>&1\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("}\r\n")

	// ── 8. QUERY SERVICE STATES ──
	b.WriteString("\r\n")
	serial("section-service-state")
	b.WriteString("Write-Output '=== HYPERV SERVICE STATE ==='\r\n")
	b.WriteString("foreach ($s in @('vmms','hvservice','vmwp','hvhost','vmcompute')) {\r\n")
	b.WriteString("    Write-Output \"-- ${s}:\"\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v Start 2>$null\r\n")
	b.WriteString("    if ($LASTEXITCODE -ne 0) { Write-Output \"  ${s}: not registered\" }\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v ImagePath 2>$null\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v Type 2>$null\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v Group 2>$null\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v DependOnService 2>$null\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v ErrorControl 2>$null\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== WSL SERVICE STATE ==='\r\n")
	b.WriteString("foreach ($s in @('LxssManager','vmcompute')) {\r\n")
	b.WriteString("    Write-Output \"-- ${s}:\"\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$s\" /v Start 2>$null\r\n")
	b.WriteString("    if ($LASTEXITCODE -ne 0) { Write-Output \"  ${s}: not registered\" }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 9. HYPERVISOR RUNTIME STATUS (before start attempt) ──
	b.WriteString("\r\n")
	serial("section-runtime")
	b.WriteString("Write-Output '=== HYPERVISOR RUNTIME STATUS ==='\r\n")
	b.WriteString("Write-Output '-- Checking driver .sys files loaded (via registry ImagePath):'\r\n")
	b.WriteString("foreach ($d in @('hvservice','winhv','winhvr','vmbus','hvsocket','vmbusr','vmbkmcl')) {\r\n")
	b.WriteString("    Write-Output \"-- ${d}:\"\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$d\" /v ImagePath 2>$null\r\n")
	b.WriteString("    if ($LASTEXITCODE -ne 0) { Write-Output \"  ${d}: not registered\" }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 10. HYPERVISOR DETECTION (ARM64-specific) ──
	b.WriteString("\r\n")
	serial("section-hypervisor-detect")
	b.WriteString("Write-Output '=== HYPERVISOR DETECTION ==='\r\n")
	b.WriteString("Write-Output '-- Virtualization-based Security state:'\r\n")
	b.WriteString("& reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\DeviceGuard\" 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- Hypervisor enforced code integrity:'\r\n")
	b.WriteString("& reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\DeviceGuard\\Scenarios\\HypervisorEnforcedCodeIntegrity\" 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- CI policy:'\r\n")
	b.WriteString("& reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\CI\" 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- Hypervisor present (HypervisorPresent from registry):'\r\n")
	b.WriteString("& reg.exe query \"HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\" /v InstallationType 2>&1\r\n")
	b.WriteString("& reg.exe query \"HKLM\\HARDWARE\\DESCRIPTION\\System\" /v SystemBiosVersion 2>&1\r\n")
	b.WriteString("& reg.exe query \"HKLM\\HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0\" 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 11. ATTEMPT TO START HYPERV SERVICES ──
	b.WriteString("\r\n")
	serial("section-start-hyperv")
	b.WriteString("Write-Output '=== START HYPERV SERVICES ==='\r\n")
	b.WriteString("foreach ($s in @('hvservice','vmbus','winhv','winhvr','hvsocket')) {\r\n")
	b.WriteString("    Write-Output \"-- net start ${s}:\"\r\n")
	b.WriteString("    & net.exe start $s 2>&1\r\n")
	b.WriteString("    Write-Output \"  exit code: $LASTEXITCODE\"\r\n")
	b.WriteString("    Write-Output \"  ${s}_NET_START_EXIT=$LASTEXITCODE\"\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- sc query service state:'\r\n")
	b.WriteString("$ScExe = $null\r\n")
	b.WriteString("if (Test-Path 'X:\\Windows\\System32\\sc.exe') { $ScExe = 'X:\\Windows\\System32\\sc.exe' }\r\n")
	b.WriteString("if (Test-Path 'X:\\windows\\system32\\sc.exe') { $ScExe = 'X:\\windows\\system32\\sc.exe' }\r\n")
	b.WriteString("if (-not $ScExe) {\r\n")
	b.WriteString("    Write-Output '  sc.exe: NOT FOUND — inject tools via WinPEDiagToolPaths'\r\n")
	b.WriteString("    foreach ($s in @('hvservice','vmbus','winhv','winhvr','hvsocket','HvHost','vmcompute')) {\r\n")
	b.WriteString("        Write-Output \"  ${s}_SC_EXIT=9009\"\r\n")
	b.WriteString("        Write-Output \"  ${s}_SC_STATE=UNAVAILABLE\"\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output \"  sc.exe: $ScExe\"\r\n")
	b.WriteString("    foreach ($s in @('hvservice','vmbus','winhv','winhvr','hvsocket','HvHost','vmcompute')) {\r\n")
	b.WriteString("        Write-Output \"-- sc query ${s}:\"\r\n")
	b.WriteString("        & $ScExe query $s >$null 2>&1\r\n")
	b.WriteString("        $ScRc = $LASTEXITCODE\r\n")
	b.WriteString("        Write-Output \"  ${s}_SC_EXIT=$ScRc\"\r\n")
	b.WriteString("        if ($ScRc -eq 0) {\r\n")
	b.WriteString("            Write-Output \"  ${s}_SC_STATE=QUERYABLE\"\r\n")
	b.WriteString("        } elseif ($ScRc -eq 1060) {\r\n")
	b.WriteString("            Write-Output \"  ${s}_SC_STATE=NOT_EXIST\"\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output \"  ${s}_SC_STATE=ERROR\"\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")
	b.WriteString("Write-Output '-- tasklist /svc (service-hosting processes):'\r\n")
	b.WriteString("$TasklistExe = $null\r\n")
	b.WriteString("if (Test-Path 'X:\\Windows\\System32\\tasklist.exe') { $TasklistExe = 'X:\\Windows\\System32\\tasklist.exe' }\r\n")
	b.WriteString("if (Test-Path 'X:\\windows\\system32\\tasklist.exe') { $TasklistExe = 'X:\\windows\\system32\\tasklist.exe' }\r\n")
	b.WriteString("if (-not $TasklistExe) { Write-Output '  tasklist: NOT FOUND' } else { & $TasklistExe /svc 2>&1 }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 12. COLLECT EVENT LOGS ──
	b.WriteString("\r\n")
	serial("section-event-logs")
	b.WriteString("Write-Output '=== EVENT LOGS ==='\r\n")
	b.WriteString("Write-Output '-- wevtutil availability:'\r\n")
	b.WriteString("$WevtutilExe = $null\r\n")
	b.WriteString("if (Test-Path 'X:\\Windows\\System32\\wevtutil.exe') { $WevtutilExe = 'X:\\Windows\\System32\\wevtutil.exe' }\r\n")
	b.WriteString("if (Test-Path 'X:\\windows\\system32\\wevtutil.exe') { $WevtutilExe = 'X:\\windows\\system32\\wevtutil.exe' }\r\n")
	b.WriteString("if (-not $WevtutilExe) {\r\n")
	b.WriteString("    Write-Output '  wevtutil: NOT FOUND'\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output \"  wevtutil: $WevtutilExe\"\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '-- Service Control Manager events (last 20):'\r\n")
	b.WriteString(`    & $WevtutilExe qe System /q:"*[System[Provider[@Name='Service Control Manager']]]" /c:20 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '-- Hyper-V related events (last 20):'\r\n")
	b.WriteString(`    & $WevtutilExe qe System /q:"*[System[Provider[starts-with(@Name,'Microsoft-Windows-Hyper-V')]]]" /c:20 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '-- Kernel-Boot events (last 10):'\r\n")
	b.WriteString(`    & $WevtutilExe qe System /q:"*[System[Provider[@Name='Microsoft-Windows-Kernel-Boot']]]" /c:10 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '-- Hyper-V-Hypervisor operational log (last 10):'\r\n")
	b.WriteString("    & $WevtutilExe qe Microsoft-Windows-Hyper-V-Hypervisor-Operational /c:10 /rd:true /f:text 2>&1\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 13. SETUPAPI LOGS ──
	b.WriteString("\r\n")
	serial("section-setupapi")
	b.WriteString("Write-Output '=== SETUPAPI LOGS ==='\r\n")
	b.WriteString("Write-Output '-- setupapi.dev.log (errors only):'\r\n")
	b.WriteString("if (Test-Path 'X:\\Windows\\inf\\setupapi.dev.log') {\r\n")
	b.WriteString("    $setupErrors = Select-String -Path 'X:\\Windows\\inf\\setupapi.dev.log' -Pattern 'ERROR|FAIL' -CaseSensitive:$false\r\n")
	b.WriteString("    if (-not $setupErrors) {\r\n")
	b.WriteString("        Write-Output '  no errors in setupapi.dev.log'\r\n")
	b.WriteString("        Write-Output '  SETUPAPI_ERRORS=NONE'\r\n")
	b.WriteString("    } else {\r\n")
	b.WriteString("        Write-Output '  SETUPAPI_ERRORS=FOUND'\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output '  setupapi.dev.log: NOT FOUND'\r\n")
	b.WriteString("    Write-Output '  SETUPAPI_ERRORS=NONE'\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 14. FINAL DRIVER STATUS (after start attempt) ──
	b.WriteString("\r\n")
	serial("section-final-status")
	b.WriteString("Write-Output '=== FINAL DRIVER STATUS ==='\r\n")
	b.WriteString("foreach ($d in @('hvservice','vmbus','winhv','winhvr','hvsocket','vmbusr','vmbkmcl','HvHost','vmcompute')) {\r\n")
	b.WriteString("    Write-Output \"-- ${d}:\"\r\n")
	b.WriteString("    & reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$d\" /v Start 2>$null\r\n")
	b.WriteString("    if ($LASTEXITCODE -ne 0) {\r\n")
	b.WriteString("        Write-Output \"  ${d}: NOT REGISTERED\"\r\n")
	b.WriteString("        Write-Output \"  ${d}_STATUS=NOT_REGISTERED\"\r\n")
	b.WriteString("    } else {\r\n")
	b.WriteString("        Write-Output \"  ${d}_STATUS=REGISTERED\"\r\n")
	b.WriteString("        $startVal = (& reg.exe query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\$d\" /v Start 2>$null | Select-String 'Start') -replace '.*\\s+', ''\r\n")
	b.WriteString("        Write-Output \"  ${d}_START_VALUE=$startVal\"\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 15. POST-MORTEM SUMMARY ──
	b.WriteString("\r\n")
	serial("section-summary")
	b.WriteString("Write-Output '=== POST-MORTEM SUMMARY ==='\r\n")
	b.WriteString("Write-Output '-- Checking if hypervisor actually launched:'\r\n")
	b.WriteString("Write-Output '   (On ARM64 TCG, hypervisor CPUID leaf is absent;'\r\n")
	b.WriteString("Write-Output '    the hypervisor requires hardware VHE support.)'\r\n")
	b.WriteString("Write-Output '-- net start (all running services):'\r\n")
	b.WriteString("& net.exe start 2>&1\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DEVCELL HYPERV DIAGNOSTICS COMPLETE ==='\r\n")
	serial("hyperv-diag-complete")

	return []byte(b.String())
}

// WinPEPayloadConfig parameterises the generated WinPE payload scripts.
type WinPEPayloadConfig struct {
	// DriverINFs are loaded with drvload before setup.exe starts. Usually
	// empty: NVMe and USB storage have inbox Windows ARM64 drivers, so
	// injection is only needed for extras like virtio-net.
	DriverINFs []string
	// ProgressPort is the guest device path for progress reporting. On ARM64
	// this must be a virtio-serial port (e.g. "\\.\Global\devcell.progress.0")
	// because PCI-serial 16550 devices don't map to user-mode COMx. Pair with
	// Spec.GuestProgressLogPath so the host can read it.
	ProgressPort string
	// WPEInit causes the bootstrap to call wpeinit before anything else.
	// Required when booting WinPE standalone (no setup.exe) — without it,
	// serial ports and other hardware are not initialized.
	WPEInit bool
	// PollSeconds is how often the agent checks for a new command (default 5).
	PollSeconds int
	// SyncAgent causes the bootstrap to run the agent synchronously (blocking)
	// instead of detached. Required when booting WinPE standalone (no
	// setup.exe): without it, winpeshl.ini returns after bootstrap.cmd and
	// WinPE reboots immediately.
	SyncAgent bool
}

// GenerateWinPEShellINI produces winpeshl.ini, which replaces WinPE's default
// startup. Entries run in order and synchronously, so the bootstrap is listed
// first and setup.exe second — dropping setup.exe here would leave WinPE with
// nothing to do after the bootstrap returns.
//
// The bootstrap is a cmd.exe script because stock WinPE lacks powershell.exe.
// The shim probes volumes for pwsh.exe and launches the real PS1 bootstrap.
func GenerateWinPEShellINI() []byte {
	return []byte("[LaunchApps]\r\n" +
		WinPEBootstrapCmdPath + "\r\n" +
		`%SYSTEMDRIVE%\setup.exe` + "\r\n")
}

// GenerateWinPEShellINI_NoSetup produces winpeshl.ini that runs ONLY the
// bootstrap — no setup.exe. Used when booting WinPE standalone (CELL-430).
func GenerateWinPEShellINI_NoSetup() []byte {
	return []byte("[LaunchApps]\r\n" +
		WinPEBootstrapCmdPath + "\r\n")
}

// GenerateWinPEBootstrapCmd produces the cmd.exe shim that winpeshl.ini calls.
// Stock WinPE has cmd.exe but not powershell.exe. This shim probes removable
// volumes for pwsh.exe (PowerShell 7, xcopy-deployed on the answer volume)
// and launches the real PowerShell bootstrap through it.
func GenerateWinPEBootstrapCmd() []byte {
	return []byte("@for %%d in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do " +
		"@if exist %%d:\\" + PwshVolDir + "\\pwsh.exe " +
		"%%d:\\" + PwshVolDir + "\\pwsh.exe -ExecutionPolicy Bypass -File " + WinPEBootstrapPath + " & goto :eof\r\n")
}

// GenerateWinPEBootstrap produces a PowerShell script that runs before
// setup.exe: initializes WinPE, loads requested drivers, opens the
// virtio-serial progress port, and launches the agent.
//
// Launched by the cmd.exe shim via pwsh.exe (PowerShell 7). Uses $PSHOME
// to locate the same pwsh.exe binary for spawning the agent.
func GenerateWinPEBootstrap(cfg WinPEPayloadConfig) []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")

	if cfg.WPEInit {
		b.WriteString("& wpeinit\r\n")
	}

	// Load drivers before writing to virtio-serial: the vioserial port
	// device doesn't exist until its driver is loaded.
	for _, inf := range cfg.DriverINFs {
		fmt.Fprintf(&b, "& drvload.exe '%s'\r\n", inf)
	}

	b.WriteString(psProgressLine(cfg, "bootstrap-start"))

	if cfg.SyncAgent {
		fmt.Fprintf(&b, "& \"$PSHOME\\pwsh.exe\" -ExecutionPolicy Bypass -File '%s'\r\n", WinPEAgentPath)
	} else {
		fmt.Fprintf(&b, "Start-Process -WindowStyle Minimized \"$PSHOME\\pwsh.exe\" "+
			"'-ExecutionPolicy Bypass -File %s'\r\n", WinPEAgentPath)
	}
	b.WriteString(psProgressLine(cfg, "agent-started"))
	return []byte(b.String())
}

// GenerateWinPEAgent produces a PowerShell control agent: a poll loop that
// snapshots Setup's logs onto the devcell volume and runs one command at a
// time, streaming output through Tee-Object to both the result file and the
// virtio-serial progress port.
//
// This exists because there is no qemu-guest-agent build for Windows ARM64
// (virtio-win ships only i386/x86_64 MSIs), so QMP guest-exec is unavailable.
// The command file lives on the removable FAT image the host also writes, and
// needs no drivers beyond inbox usbstor.
func GenerateWinPEAgent(cfg WinPEPayloadConfig) []byte {
	poll := cfg.PollSeconds
	if poll <= 0 {
		poll = 5
	}

	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")

	// Accept volume as argument (answer-volume launcher already knows the
	// letter); fall back to marker search.
	b.WriteString("$DevcellVol = if ($args.Count -gt 0) { $args[0] } else { $null }\r\n")
	b.WriteString("\r\n")

	// Find the devcell volume by marker file.
	b.WriteString("while (-not $DevcellVol) {\r\n")
	fmt.Fprintf(&b, "    foreach ($d in 'C','D','E','F','G','H','I','J','K','L','M','N','O','P','Q','R','S','T','U','V','W','Y','Z') {\r\n")
	fmt.Fprintf(&b, "        if (Test-Path \"${d}:\\%s\") { $DevcellVol = \"${d}:\"; break }\r\n", AgentVolumeMarker)
	b.WriteString("    }\r\n")
	fmt.Fprintf(&b, "    if (-not $DevcellVol) { Start-Sleep -Seconds %d }\r\n", poll)
	b.WriteString("}\r\n")
	b.WriteString(psProgressLine(cfg, "agent-volume $DevcellVol"))
	b.WriteString("\r\n")

	// Open the virtio-serial progress port for streaming if configured.
	if cfg.ProgressPort != "" {
		b.WriteString("$progressStream = $null\r\n")
		b.WriteString("try {\r\n")
		fmt.Fprintf(&b, "    $progressStream = [System.IO.File]::Open('%s',\r\n", cfg.ProgressPort)
		b.WriteString("        [System.IO.FileMode]::Open,\r\n")
		b.WriteString("        [System.IO.FileAccess]::Write,\r\n")
		b.WriteString("        [System.IO.FileShare]::ReadWrite)\r\n")
		b.WriteString("} catch { }\r\n")
		b.WriteString("\r\n")
	}

	// Poll loop.
	b.WriteString("while ($true) {\r\n")

	// Snapshot Setup's logs (X: RAM disk -> answer volume, CELL-364).
	fmt.Fprintf(&b, "    Copy-Item 'X:\\Windows\\Panther\\setupact.log' \"$DevcellVol\\%s\" -Force -ErrorAction SilentlyContinue\r\n", SetupActSnapshotName)
	fmt.Fprintf(&b, "    Copy-Item 'X:\\Windows\\Panther\\setuperr.log' \"$DevcellVol\\%s\" -Force -ErrorAction SilentlyContinue\r\n", SetupErrSnapshotName)
	fmt.Fprintf(&b, "    Copy-Item 'X:\\$windows.~bt\\Sources\\Panther\\setupact.log' \"$DevcellVol\\%s\" -Force -ErrorAction SilentlyContinue\r\n", SetupActSnapshotName)
	fmt.Fprintf(&b, "    Copy-Item 'X:\\$windows.~bt\\Sources\\Panther\\setuperr.log' \"$DevcellVol\\%s\" -Force -ErrorAction SilentlyContinue\r\n", SetupErrSnapshotName)
	b.WriteString("\r\n")

	// Check for a command file.
	fmt.Fprintf(&b, "    $cmdFile = \"$DevcellVol\\%s\"\r\n", AgentCommandFile)
	b.WriteString("    if (Test-Path $cmdFile) {\r\n")
	b.WriteString("        $devcellCmd = (Get-Content $cmdFile -Raw).Trim()\r\n")
	// Delete first: a command that reboots or hangs must not re-run forever.
	b.WriteString("        Remove-Item $cmdFile -Force\r\n")
	fmt.Fprintf(&b, "        $resultFile = \"$DevcellVol\\%s\"\r\n", AgentResultFile)
	b.WriteString("        try {\r\n")
	if cfg.ProgressPort != "" {
		b.WriteString("            Invoke-Expression $devcellCmd 2>&1 | Tee-Object -FilePath $resultFile | ForEach-Object {\r\n")
		b.WriteString("                if ($progressStream) {\r\n")
		b.WriteString("                    $bytes = [System.Text.Encoding]::UTF8.GetBytes(\"$_`n\")\r\n")
		b.WriteString("                    $progressStream.Write($bytes, 0, $bytes.Length)\r\n")
		b.WriteString("                    $progressStream.Flush()\r\n")
		b.WriteString("                }\r\n")
		b.WriteString("                $_\r\n")
		b.WriteString("            }\r\n")
	} else {
		b.WriteString("            Invoke-Expression $devcellCmd 2>&1 | Set-Content $resultFile -Encoding UTF8\r\n")
	}
	b.WriteString("        } catch {\r\n")
	b.WriteString("            \"ERROR: $($_.Exception.Message)\" | Set-Content $resultFile -Encoding UTF8\r\n")
	b.WriteString("        }\r\n")
	fmt.Fprintf(&b, "        'done' | Set-Content \"$DevcellVol\\%s\" -Encoding UTF8\r\n", AgentDoneFile)
	b.WriteString(psProgressLineIndent(cfg, "ran $devcellCmd", "        "))
	b.WriteString("    }\r\n")
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "    Start-Sleep -Seconds %d\r\n", poll)
	b.WriteString("}\r\n")

	return []byte(b.String())
}

// WinPEEchoProbeScriptCommand returns the agent command that invokes the
// COM-port echo probe script.
func WinPEEchoProbeScriptCommand() string {
	return `& "$DevcellVol\` + WinPEEchoProbeScriptName + `" $DevcellVol`
}

// GenerateWinPEEchoProbeScript produces a WinPE PowerShell script that:
//  1. Probes COM1 through COM4, echoing a unique marker to each port so the
//     host can determine which serial device maps to PCI-serial on ARM64.
//  2. Loads the viofs driver via drvload and mounts a virtiofs share using
//     virtiofs.exe, then writes a test file to the mount point.
//
// The answer volume path is passed as $args[0].
// viofs driver files and virtiofs.exe are expected under $Vol\drivers\viofs\.
// The virtiofs tag must match Spec.VirtioFSTag (default "devcell-logs").
func GenerateWinPEEchoProbeScript(viofsTag string) []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Vol = $args[0]\r\n")
	b.WriteString("\r\n")

	// Section 1: COM port probe
	b.WriteString("Write-Output '===== COM PORT PROBE ====='\r\n")
	for i := 1; i <= 4; i++ {
		marker := fmt.Sprintf("DEVCELL_COM_ECHO_COM%d", i)
		fmt.Fprintf(&b, "try {\r\n")
		fmt.Fprintf(&b, "    [System.IO.File]::WriteAllText('COM%d', '%s')\r\n", i, marker)
		fmt.Fprintf(&b, "    Write-Output 'COM%d: OK'\r\n", i)
		fmt.Fprintf(&b, "} catch {\r\n")
		fmt.Fprintf(&b, "    Write-Output 'COM%d: FAILED'\r\n", i)
		fmt.Fprintf(&b, "}\r\n")
	}
	b.WriteString("Write-Output '===== COM PROBE DONE ====='\r\n")
	b.WriteString("\r\n")

	// Section 2: viofs driver load + virtiofs mount
	b.WriteString("Write-Output '===== VIOFS MOUNT ====='\r\n")

	b.WriteString("if (Test-Path \"$Vol\\drivers\\viofs\\viofs.inf\") {\r\n")
	b.WriteString("    & drvload.exe \"$Vol\\drivers\\viofs\\viofs.inf\"\r\n")
	b.WriteString("    Write-Output \"drvload viofs: $LASTEXITCODE\"\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output 'viofs.inf not found — skipping driver load'\r\n")
	b.WriteString("}\r\n")

	// Wait a moment for PnP to settle
	b.WriteString("Start-Sleep -Seconds 3\r\n")

	mountLetter := "V:"
	b.WriteString("if (Test-Path \"$Vol\\drivers\\viofs\\virtiofs.exe\") {\r\n")
	fmt.Fprintf(&b, "    & \"$Vol\\drivers\\viofs\\virtiofs.exe\" mount -t %s %s\r\n", viofsTag, mountLetter)
	b.WriteString("    if ($LASTEXITCODE -ne 0) {\r\n")
	b.WriteString("        Write-Output 'virtiofs mount: FAILED'\r\n")
	b.WriteString("    } else {\r\n")
	b.WriteString("        Write-Output 'virtiofs mount: OK'\r\n")
	fmt.Fprintf(&b, "        Set-Content '%s\\viofs-probe.txt' 'DEVCELL_VIOFS_HELLO'\r\n", mountLetter)
	fmt.Fprintf(&b, "        if (Test-Path '%s\\viofs-probe.txt') {\r\n", mountLetter)
	b.WriteString("            Write-Output 'viofs write: OK'\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output 'viofs write: FAILED'\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output 'virtiofs.exe not found — skipping mount'\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output '===== VIOFS DONE ====='\r\n")
	b.WriteString("\r\n")

	b.WriteString("Write-Output 'DEVCELL ECHO PROBE COMPLETE'\r\n")

	// go-diskfs v1.9.4 records the cluster-rounded size in the directory
	// entry instead of the actual file size, so reads return trailing
	// garbage for any file not cluster-aligned. Pad to a 512-byte boundary.
	if rem := b.Len() % 512; rem != 0 {
		b.WriteString("# ")
		for b.Len()%512 != 0 {
			b.WriteByte('.')
		}
	}

	return []byte(b.String())
}

// psProgressLine emits a PowerShell line that writes to the progress port.
func psProgressLine(cfg WinPEPayloadConfig, msg string) string {
	if cfg.ProgressPort == "" {
		return ""
	}
	return fmt.Sprintf("\"devcell: %s\" | Out-File -Append '%s' -Encoding utf8\r\n", msg, cfg.ProgressPort)
}

// psProgressLineIndent is psProgressLine with a custom prefix for nested blocks.
func psProgressLineIndent(cfg WinPEPayloadConfig, msg, indent string) string {
	if cfg.ProgressPort == "" {
		return ""
	}
	return fmt.Sprintf("%s\"devcell: %s\" | Out-File -Append '%s' -Encoding utf8\r\n", indent, msg, cfg.ProgressPort)
}
