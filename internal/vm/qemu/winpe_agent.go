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
	// WinPEBootstrapPath runs once at WinPE startup, before setup.exe.
	WinPEBootstrapPath = `X:\devcell\bootstrap.cmd`
	// WinPEAgentPath is the control agent, started detached by the bootstrap.
	WinPEAgentPath = `X:\devcell\agent.cmd`

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
	AgentScriptName = `devcell-agent.cmd`
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
// (0x80070001 - 0x40030, run 20260729T172019), so this is built from parts
// that cannot: `if exist` letter probing, a detached `start` (Setup is never
// blocked), and a forced `exit /b 0`.
func WinPEAgentLauncherCommand() string {
	return `cmd /c (for %l in (C D E F G H I J K L) do @if exist %l:\` + AgentScriptName +
		` start "devcell-agent" /min cmd /c %l:\` + AgentScriptName + ` %l:) & exit /b 0`
}

// WinPEDriverLoadCommand returns a windowsPE RunSynchronous command that
// drvloads one INF from whatever drive letter the answer volume received.
//
// This is the last hook before Modern Setup searches for install media:
// run 20260812T150644 logged "WinPEInitialization: Leaving Execute Method"
// and "EarlyF6DriverInstall: Entering Execute Method" one second apart, in
// that order. The agent's poll loop is too late — its drvload landed after
// the media search had already failed (0x80070103, run 20260812T143146).
//
// The shape is copied verbatim from WinPEAgentLauncherCommand, which that
// same log shows executing with exit code 0x00000000: one `if exist` inside
// the letter probe, no nested parentheses, and a forced `exit /b 0`.
func WinPEDriverLoadCommand(inf string) string {
	return `cmd /c (for %l in (C D E F G H I J K L) do @if exist %l:\` + inf +
		` drvload %l:\` + inf + `) & exit /b 0`
}

// WinPEDiagCommand is the one-shot diagnostic the agent executes when a
// build ships it as AgentCommand; its combined output lands in
// devcell-out.txt on the answer volume. Strictly read-only: the first
// version drvloaded vioscsi and collided with wpeinit's own $WinPEDriver$
// load — Setup aborted 0x80070103 ERROR_NO_MORE_ITEMS, run 20260812T143146.
//
// Deprecated: prefer WinPEDiagScriptCommand, which invokes the proper
// diagnostics script and waits for completion before the output is read.
const WinPEDiagCommand = `echo list volume> X:\devcell-lv.txt & echo exit>> X:\devcell-lv.txt & diskpart /s X:\devcell-lv.txt & reg query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi & dir X:\Windows\Panther X:\$windows.~bt\Sources\Panther`

const (
	// WinPEDiagScriptName is the diagnostics script shipped on the answer
	// volume. It follows the same structured-output pattern as
	// GenerateGuestDiagnosticsScript (guest_diagnostics.go) but runs in
	// WinPE where PowerShell may not be available. It tries PowerShell
	// first for Get-Volume (richer output), falls back to diskpart.
	WinPEDiagScriptName = `devcell-winpe-diag.cmd`
)

// WinPEDiagScriptCommand returns the agent command that invokes the
// diagnostics script. Uses %DEVCELL_VOL% (percent expansion), not
// !DEVCELL_VOL! (delayed expansion), because the agent reads this via
// `set /p` which strips ! characters when enabledelayedexpansion is active.
func WinPEDiagScriptCommand() string {
	return `%DEVCELL_VOL%\` + WinPEDiagScriptName + ` %DEVCELL_VOL%`
}

// GenerateWinPEDiagScript produces the WinPE diagnostics script. It is
// shipped on the answer volume and invoked by the agent. Output goes to
// stdout (the agent redirects it to AgentResultFile).
//
// Three sections:
//  1. Disk/volume enumeration — diskpart (always available) plus wmic if present
//  2. PowerShell probe — is it available, can it run as admin, Get-Volume output
//  3. Script access — can we see other devcell scripts on the answer volume
func GenerateWinPEDiagScript() []byte {
	return []byte(`@echo off
setlocal enabledelayedexpansion
set VOL=%1

echo === DEVCELL WINPE DIAGNOSTICS ===
echo %DATE% %TIME%
echo Volume: %VOL%
echo.

rem ── 0. CPU / PROCESSOR CAPABILITIES ────────────────────────────────
echo === PROCESSOR INFO ===
echo PROCESSOR_ARCHITECTURE=%PROCESSOR_ARCHITECTURE%
echo PROCESSOR_IDENTIFIER=%PROCESSOR_IDENTIFIER%
echo PROCESSOR_LEVEL=%PROCESSOR_LEVEL%
echo PROCESSOR_REVISION=%PROCESSOR_REVISION%
echo NUMBER_OF_PROCESSORS=%NUMBER_OF_PROCESSORS%
echo.

echo === CPU REGISTRY ===
reg query "HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0" 2>nul
echo.

echo === WMIC CPU (full) ===
wmic cpu get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic cpu: not available
echo.

echo === WMIC COMPUTERSYSTEM ===
wmic computersystem get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic computersystem: not available
echo.

echo === WMIC BASEBOARD ===
wmic baseboard get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic baseboard: not available
echo.

echo === WMIC BIOS ===
wmic bios get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic bios: not available
echo.

echo === WMIC MEMORYCHIP ===
wmic memorychip get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic memorychip: not available
echo.

echo === WMIC OS ===
wmic os get /format:list 2>nul
if %ERRORLEVEL% neq 0 echo wmic os: not available
echo.

echo === SYSTEMINFO ===
systeminfo 2>nul
if %ERRORLEVEL% neq 0 echo systeminfo: not available
echo.

rem ── 1. DISK CHECKS ──────────────────────────────────────────────────
echo === DISKPART VOLUMES ===
echo list volume> X:\devcell-lv.txt
echo exit>> X:\devcell-lv.txt
diskpart /s X:\devcell-lv.txt
echo.

echo === DISKPART DISKS ===
echo list disk> X:\devcell-ld.txt
echo exit>> X:\devcell-ld.txt
diskpart /s X:\devcell-ld.txt
echo.

echo === WMIC LOGICALDISK ===
wmic logicaldisk get caption,description,filesystem,volumename,size 2>nul
if %ERRORLEVEL% neq 0 echo wmic: not available
echo.

echo === STORAGE DRIVERS ===
echo -- USBSTOR:
reg query HKLM\SYSTEM\CurrentControlSet\Services\USBSTOR /v Start 2>nul
if %ERRORLEVEL% neq 0 echo   not loaded
echo -- vioscsi:
reg query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi /v Start 2>nul
if %ERRORLEVEL% neq 0 echo   not loaded
echo -- viostor:
reg query HKLM\SYSTEM\CurrentControlSet\Services\viostor /v Start 2>nul
if %ERRORLEVEL% neq 0 echo   not loaded
echo -- storahci:
reg query HKLM\SYSTEM\CurrentControlSet\Services\storahci /v Start 2>nul
if %ERRORLEVEL% neq 0 echo   not loaded
echo.

rem ── 2. POWERSHELL PROBE ─────────────────────────────────────────────
echo === POWERSHELL AVAILABILITY ===
where powershell >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo powershell.exe: NOT FOUND on PATH
    echo WinPE optional component WinPE-PowerShell is not installed.
    goto skip_ps
)
echo powershell.exe: found
echo.
echo === POWERSHELL VERSION ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "$PSVersionTable | Format-List" 2>&1
echo.
echo === POWERSHELL ADMIN CHECK ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)" 2>&1
echo.
echo === POWERSHELL GET-VOLUME ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-Volume | Format-Table DriveLetter, FileSystemLabel, DriveType, FileSystem, @{N='SizeGB';E={[math]::Round($_.Size/1GB,1)}} -AutoSize | Out-String -Width 200" 2>&1
echo.
echo === POWERSHELL GET-DISK ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-Disk | Format-Table Number, FriendlyName, BusType, Size, PartitionStyle -AutoSize | Out-String -Width 200" 2>&1
echo.
echo === POWERSHELL CPU (full) ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-CimInstance Win32_Processor | Format-List *" 2>&1
echo.
echo === POWERSHELL COMPUTERSYSTEM ===
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-CimInstance Win32_ComputerSystem | Format-List *" 2>&1
echo.
:skip_ps

rem ── 3. SCRIPT ACCESS ────────────────────────────────────────────────
echo === ANSWER VOLUME CONTENTS ===
if "%VOL%"=="" (
    echo VOL not set, skipping
    goto skip_vol
)
dir %VOL%\ 2>nul
echo.
echo === DEVCELL SCRIPTS ===
for %%f in (` + AgentScriptName + ` ` + AgentVolumeMarker + ` ` + BootstrapScriptName + ` autounattend.xml) do (
    if exist %VOL%\%%f (
        echo [OK]    %VOL%\%%f
    ) else (
        echo [MISS]  %VOL%\%%f
    )
)
echo.
:skip_vol

echo === PANTHER LOGS ===
dir X:\Windows\Panther 2>nul
echo ---
dir X:\$windows.~bt\Sources\Panther 2>nul
echo.

echo === DEVCELL DIAGNOSTICS COMPLETE ===
`)
}

const (
	// WinPEHyperVDiagScriptName is the diagnostics script that probes
	// Hyper-V and WSL2 feature/service state inside WinPE. It mounts
	// install.wim from the attached Windows ISO, uses DISM to enable
	// features offline, loads the offline registry to enable services,
	// then queries their state after reloading.
	WinPEHyperVDiagScriptName = `devcell-winpe-hyperv-diag.cmd`

	// WinPEEchoProbeScriptName is the filename for the COM-port echo
	// probe + virtiofs write test script.
	WinPEEchoProbeScriptName = `devcell-winpe-echo-probe.cmd`
)

// WinPEHyperVDiagScriptCommand returns the agent command that invokes the
// Hyper-V/WSL2 diagnostics script.
func WinPEHyperVDiagScriptCommand() string {
	return `%DEVCELL_VOL%\` + WinPEHyperVDiagScriptName + ` %DEVCELL_VOL%`
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
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal enabledelayedexpansion\r\n")
	b.WriteString("set VOL=%1\r\n")

	serial := func(msg string) {
		if progressPort != "" {
			fmt.Fprintf(&b, "echo devcell: %s >%s\r\n", msg, progressPort)
		}
	}

	serial("hyperv-diag-start")
	b.WriteString("\r\n")
	b.WriteString("echo === DEVCELL HYPERV DIAGNOSTICS ===\r\n")
	b.WriteString("echo %DATE% %TIME%\r\n")
	b.WriteString("echo Volume: %VOL%\r\n")
	b.WriteString("echo.\r\n")

	// ── 1. SYSTEM / ARCHITECTURE INFO ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 1. SYSTEM INFO\r\n")
	serial("section-sysinfo")
	b.WriteString("echo === SYSTEM INFO ===\r\n")
	b.WriteString("echo -- PROCESSOR_ARCHITECTURE: %PROCESSOR_ARCHITECTURE%\r\n")
	b.WriteString("echo -- PROCESSOR_IDENTIFIER:   %PROCESSOR_IDENTIFIER%\r\n")
	b.WriteString("echo -- NUMBER_OF_PROCESSORS:    %NUMBER_OF_PROCESSORS%\r\n")
	b.WriteString("echo -- OS version:\r\n")
	b.WriteString("ver 2>&1\r\n")
	b.WriteString("echo -- systeminfo (if available):\r\n")
	b.WriteString("where systeminfo >nul 2>&1 && systeminfo 2>&1 || echo   systeminfo: NOT FOUND\r\n")
	b.WriteString("echo.\r\n")

	// ── 2. BCD HYPERVISOR CONFIGURATION ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 2. BCD HYPERVISOR CONFIGURATION\r\n")
	serial("section-bcd")
	b.WriteString("echo === BCD HYPERVISOR CONFIG ===\r\n")
	b.WriteString("echo -- bcdedit /enum {current}:\r\n")
	b.WriteString("bcdedit /enum {current} 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- bcdedit /enum {hypervisorsettings}:\r\n")
	b.WriteString("bcdedit /enum {hypervisorsettings} 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- bcdedit /enum ALL (full BCD store):\r\n")
	b.WriteString("bcdedit /enum ALL 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// ── 3. HYPERVISOR HOST BINARIES ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 3. HYPERVISOR HOST BINARIES\r\n")
	serial("section-binaries")
	b.WriteString("echo === HYPERVISOR HOST BINARIES ===\r\n")
	b.WriteString("set BINARIES_OK=0\r\n")
	b.WriteString("set BINARIES_MISSING=0\r\n")
	b.WriteString("\r\n")
	b.WriteString("for %%f in (\r\n")
	b.WriteString("    X:\\Windows\\System32\\hvaa64.exe\r\n")
	b.WriteString("    X:\\Windows\\System32\\hvloader.dll\r\n")
	b.WriteString("    X:\\Windows\\System32\\hvhostsvc.dll\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\hvservice.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\winhv.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\winhvr.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\hvsocket.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\vmbus.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\HvSocket.dll\r\n")
	b.WriteString("    X:\\Windows\\System32\\bcdedit.exe\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\vmbusr.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\drivers\\vmbkmcl.sys\r\n")
	b.WriteString("    X:\\Windows\\System32\\vmms.exe\r\n")
	b.WriteString("    X:\\Windows\\System32\\vmwp.exe\r\n")
	b.WriteString("    X:\\Windows\\System32\\vmcompute.exe\r\n")
	b.WriteString(") do (\r\n")
	b.WriteString("    if exist %%f (\r\n")
	b.WriteString("        echo   FOUND: %%f\r\n")
	b.WriteString("        set /a BINARIES_OK+=1\r\n")
	b.WriteString("    ) else (\r\n")
	b.WriteString("        echo   MISSING: %%f\r\n")
	b.WriteString("        set /a BINARIES_MISSING+=1\r\n")
	b.WriteString("    )\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo Binaries found: !BINARIES_OK!  missing: !BINARIES_MISSING!\r\n")
	serial("binaries-ok=!BINARIES_OK! missing=!BINARIES_MISSING!")
	b.WriteString("echo.\r\n")

	// ── 4. HYPERVISOR DRIVER REGISTRY DETAILS ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 4. HYPERVISOR DRIVER REGISTRY DETAILS\r\n")
	serial("section-driver-registry")
	b.WriteString("echo === DRIVER REGISTRY DETAILS ===\r\n")
	b.WriteString("for %%s in (hvservice winhv winhvr vmbus hvsocket vmbusr vmbkmcl) do (\r\n")
	b.WriteString("    echo -- %%s full registry:\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" 2>&1\r\n")
	b.WriteString("    echo.\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 5. HYPERVISOR DRIVER STATE (registry-based, driverquery hangs under TCG) ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 5. HYPERVISOR DRIVER STATE\r\n")
	serial("section-driverquery")
	b.WriteString("echo === HYPERVISOR DRIVER STATE ===\r\n")
	b.WriteString("echo -- Driver Start values from CurrentControlSet (0=Boot 1=System 2=Auto 3=Manual 4=Disabled):\r\n")
	b.WriteString("for %%d in (hvservice winhv winhvr vmbus hvsocket vmbusr vmbkmcl) do (\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%d\" /v Start 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 echo   %%d: not registered\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 6. DISM ONLINE PACKAGES ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 6. DISM ONLINE PACKAGES\r\n")
	serial("section-dism")
	b.WriteString("echo === DISM ONLINE PACKAGES ===\r\n")
	b.WriteString("echo -- All packages:\r\n")
	b.WriteString("dism /Online /Get-Packages 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- DISM features (if available):\r\n")
	b.WriteString("dism /Online /Get-Features 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// ── 7. OFFLINE SERVICE ENABLEMENT ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 7. OFFLINE SERVICE ENABLEMENT\r\n")
	serial("section-offline-registry")
	b.WriteString("echo === OFFLINE SERVICE ENABLE ===\r\n")
	b.WriteString("echo -- Loading offline SYSTEM hive:\r\n")
	b.WriteString("reg load HKLM\\OFFLINE X:\\Windows\\System32\\config\\SYSTEM 2>&1\r\n")
	b.WriteString("if %ERRORLEVEL% neq 0 (\r\n")
	b.WriteString("    echo ERROR: failed to load SYSTEM hive\r\n")
	b.WriteString("    goto services\r\n")
	b.WriteString(")\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Querying existing hvservice Start value:\r\n")
	b.WriteString("reg query \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /v Start 2>&1\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Setting vmms service Start=2 (Auto):\r\n")
	b.WriteString("reg add \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmms\" /v Start /t REG_DWORD /d 2 /f 2>&1\r\n")
	b.WriteString("echo exit code: %ERRORLEVEL%\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Setting hvservice Start=0 (Boot):\r\n")
	b.WriteString("reg add \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /v Start /t REG_DWORD /d 0 /f 2>&1\r\n")
	b.WriteString("echo exit code: %ERRORLEVEL%\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Setting vmwp Start=3 (Manual):\r\n")
	b.WriteString("reg add \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmwp\" /v Start /t REG_DWORD /d 3 /f 2>&1\r\n")
	b.WriteString("echo exit code: %ERRORLEVEL%\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Listing all Hyper-V related services in hive:\r\n")
	b.WriteString("for %%s in (hvservice vmms vmwp vmcompute hvhost winhv winhvr vmbus hvsocket vmbusr vmbkmcl) do (\r\n")
	b.WriteString("    reg query \"HKLM\\OFFLINE\\ControlSet001\\Services\\%%s\" /v Start 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! equ 0 (\r\n")
	b.WriteString("        echo   %%s: present\r\n")
	b.WriteString("    ) else (\r\n")
	b.WriteString("        echo   %%s: not in hive\r\n")
	b.WriteString("    )\r\n")
	b.WriteString(")\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Full offline hvservice key:\r\n")
	b.WriteString("reg query \"HKLM\\OFFLINE\\ControlSet001\\Services\\hvservice\" /s 2>&1\r\n")
	b.WriteString("echo -- Full offline vmbus key:\r\n")
	b.WriteString("reg query \"HKLM\\OFFLINE\\ControlSet001\\Services\\vmbus\" /s 2>&1\r\n")
	b.WriteString("echo -- Full offline winhv key:\r\n")
	b.WriteString("reg query \"HKLM\\OFFLINE\\ControlSet001\\Services\\winhv\" /s 2>&1\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo -- Unloading offline hive:\r\n")
	b.WriteString("reg unload HKLM\\OFFLINE 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// ── 8. QUERY SERVICE STATES ──
	b.WriteString("\r\n")
	b.WriteString(":services\r\n")
	b.WriteString("rem -- 8. QUERY SERVICE STATES (via registry — sc.exe absent in WinPE ARM64)\r\n")
	serial("section-service-state")
	b.WriteString("echo === HYPERV SERVICE STATE ===\r\n")
	b.WriteString("for %%s in (vmms hvservice vmwp hvhost vmcompute) do (\r\n")
	b.WriteString("    echo -- %%s:\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v Start 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 echo   %%s: not registered\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v ImagePath 2>nul\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v Type 2>nul\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v Group 2>nul\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v DependOnService 2>nul\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v ErrorControl 2>nul\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("\r\n")
	b.WriteString("echo === WSL SERVICE STATE ===\r\n")
	b.WriteString("for %%s in (LxssManager vmcompute) do (\r\n")
	b.WriteString("    echo -- %%s:\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%s\" /v Start 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 echo   %%s: not registered\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 9. HYPERVISOR RUNTIME STATUS (before start attempt) ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 9. HYPERVISOR RUNTIME STATUS (before start attempt)\r\n")
	serial("section-runtime")
	b.WriteString("echo === HYPERVISOR RUNTIME STATUS ===\r\n")
	b.WriteString("echo -- Checking driver .sys files loaded (via registry ImagePath):\r\n")
	b.WriteString("for %%d in (hvservice winhv winhvr vmbus hvsocket vmbusr vmbkmcl) do (\r\n")
	b.WriteString("    echo -- %%d:\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%d\" /v ImagePath 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 echo   %%d: not registered\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 10. HYPERVISOR DETECTION (ARM64-specific) ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 10. HYPERVISOR DETECTION\r\n")
	serial("section-hypervisor-detect")
	b.WriteString("echo === HYPERVISOR DETECTION ===\r\n")
	b.WriteString("echo -- Virtualization-based Security state:\r\n")
	b.WriteString("reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\DeviceGuard\" 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Hypervisor enforced code integrity:\r\n")
	b.WriteString("reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\DeviceGuard\\Scenarios\\HypervisorEnforcedCodeIntegrity\" 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- CI policy:\r\n")
	b.WriteString("reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Control\\CI\" 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Hypervisor present (HypervisorPresent from registry):\r\n")
	b.WriteString("reg query \"HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\" /v InstallationType 2>&1\r\n")
	b.WriteString("reg query \"HKLM\\HARDWARE\\DESCRIPTION\\System\" /v SystemBiosVersion 2>&1\r\n")
	b.WriteString("reg query \"HKLM\\HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0\" 2>&1\r\n")
	b.WriteString("echo.\r\n")

	// ── 11. ATTEMPT TO START HYPERV SERVICES ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 11. ATTEMPT TO START HYPERV SERVICES\r\n")
	serial("section-start-hyperv")
	b.WriteString("echo === START HYPERV SERVICES ===\r\n")
	b.WriteString("for %%s in (hvservice vmbus winhv winhvr hvsocket) do (\r\n")
	b.WriteString("    echo -- net start %%s:\r\n")
	b.WriteString("    net start %%s 2>&1\r\n")
	b.WriteString("    echo   exit code: !ERRORLEVEL!\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 12. COLLECT EVENT LOGS ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 12. COLLECT EVENT LOGS\r\n")
	serial("section-event-logs")
	b.WriteString("echo === EVENT LOGS ===\r\n")
	b.WriteString("echo -- wevtutil availability:\r\n")
	b.WriteString("where wevtutil >nul 2>&1\r\n")
	b.WriteString("if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("    echo   wevtutil: NOT FOUND\r\n")
	b.WriteString("    goto skip_evtlog\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo   wevtutil: found\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Service Control Manager events (last 20):\r\n")
	b.WriteString(`wevtutil qe System /q:"*[System[Provider[@Name='Service Control Manager']]]" /c:20 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Hyper-V related events (last 20):\r\n")
	b.WriteString(`wevtutil qe System /q:"*[System[Provider[starts-with(@Name,'Microsoft-Windows-Hyper-V')]]]" /c:20 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Kernel-Boot events (last 10):\r\n")
	b.WriteString(`wevtutil qe System /q:"*[System[Provider[@Name='Microsoft-Windows-Kernel-Boot']]]" /c:10 /rd:true /f:text 2>&1` + "\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString("echo -- Hyper-V-Hypervisor operational log (last 10):\r\n")
	b.WriteString("wevtutil qe Microsoft-Windows-Hyper-V-Hypervisor-Operational /c:10 /rd:true /f:text 2>&1\r\n")
	b.WriteString("echo.\r\n")
	b.WriteString(":skip_evtlog\r\n")
	b.WriteString("echo.\r\n")

	// ── 13. SETUPAPI LOGS ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 13. SETUPAPI LOGS\r\n")
	serial("section-setupapi")
	b.WriteString("echo === SETUPAPI LOGS ===\r\n")
	b.WriteString("echo -- setupapi.dev.log (errors only):\r\n")
	b.WriteString("if exist X:\\Windows\\inf\\setupapi.dev.log (\r\n")
	b.WriteString("    findstr /i \"ERROR FAIL\" X:\\Windows\\inf\\setupapi.dev.log 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 echo   no errors in setupapi.dev.log\r\n")
	b.WriteString(") else (\r\n")
	b.WriteString("    echo   setupapi.dev.log: NOT FOUND\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 14. FINAL DRIVER STATUS (after start attempt) ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 14. FINAL DRIVER STATUS (after start attempt)\r\n")
	serial("section-final-status")
	b.WriteString("echo === FINAL DRIVER STATUS ===\r\n")
	b.WriteString("for %%d in (hvservice vmbus winhv winhvr hvsocket vmbusr vmbkmcl) do (\r\n")
	b.WriteString("    echo -- %%d:\r\n")
	b.WriteString("    reg query \"HKLM\\SYSTEM\\CurrentControlSet\\Services\\%%d\" /v Start 2>nul\r\n")
	b.WriteString("    if !ERRORLEVEL! neq 0 (\r\n")
	b.WriteString("        echo   %%d: NOT REGISTERED\r\n")
	b.WriteString("        echo   %%d_STATUS=NOT_REGISTERED\r\n")
	b.WriteString("    ) else (\r\n")
	b.WriteString("        echo   %%d_STATUS=REGISTERED\r\n")
	b.WriteString("    )\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo.\r\n")

	// ── 15. POST-MORTEM SUMMARY ──
	b.WriteString("\r\n")
	b.WriteString("rem -- 15. POST-MORTEM SUMMARY\r\n")
	serial("section-summary")
	b.WriteString("echo === POST-MORTEM SUMMARY ===\r\n")
	b.WriteString("echo -- Checking if hypervisor actually launched:\r\n")
	b.WriteString("echo    (On ARM64 TCG, hypervisor CPUID leaf is absent;\r\n")
	b.WriteString("echo     the hypervisor requires hardware VHE support.)\r\n")
	b.WriteString("echo -- net start (all running services):\r\n")
	b.WriteString("net start 2>&1\r\n")
	b.WriteString("echo.\r\n")

	b.WriteString("\r\n")
	b.WriteString(":done\r\n")
	b.WriteString("echo === DEVCELL HYPERV DIAGNOSTICS COMPLETE ===\r\n")
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
func GenerateWinPEShellINI() []byte {
	return []byte("[LaunchApps]\r\n" +
		"cmd.exe, /c " + WinPEBootstrapPath + "\r\n" +
		`%SYSTEMDRIVE%\setup.exe` + "\r\n")
}

// GenerateWinPEShellINI_NoSetup produces winpeshl.ini that runs ONLY the
// bootstrap — no setup.exe. Used when booting WinPE standalone (CELL-430).
func GenerateWinPEShellINI_NoSetup() []byte {
	return []byte("[LaunchApps]\r\n" +
		"cmd.exe, /c " + WinPEBootstrapPath + "\r\n")
}

// GenerateWinPEBootstrap produces the script that runs before setup.exe:
// loads any requested drivers, starts the agent detached, and reports
// progress to the host.
func GenerateWinPEBootstrap(cfg WinPEPayloadConfig) []byte {
	var b strings.Builder
	b.WriteString("@echo off\r\n")

	if cfg.WPEInit {
		b.WriteString("wpeinit\r\n")
	}

	b.WriteString(progressLine(cfg, "bootstrap-start"))

	for _, inf := range cfg.DriverINFs {
		fmt.Fprintf(&b, "drvload %s\r\n", inf)
		b.WriteString(progressLine(cfg, "drvload "+inf))
	}

	if cfg.SyncAgent {
		fmt.Fprintf(&b, "call %s\r\n", WinPEAgentPath)
	} else {
		// winpeshl runs entries synchronously; without `start` the agent's poll
		// loop would never return and setup.exe would never launch.
		fmt.Fprintf(&b, "start \"devcell-agent\" /min cmd /c %s\r\n", WinPEAgentPath)
	}
	b.WriteString(progressLine(cfg, "agent-started"))
	return []byte(b.String())
}

// GenerateWinPEAgent produces the control agent: a poll loop that snapshots
// Setup's logs onto the devcell volume and runs one command at a time from
// it, writing the output back.
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
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal enabledelayedexpansion\r\n")
	// The answer-volume launcher passes the volume it found the agent on;
	// the marker search below is the fallback for the boot.wim path, where
	// the agent starts with no argument.
	b.WriteString("set DEVCELL_VOL=\r\n")
	b.WriteString("if not \"%1\"==\"\" set DEVCELL_VOL=%1\r\n")
	b.WriteString(":find\r\n")
	b.WriteString("if not \"!DEVCELL_VOL!\"==\"\" goto found\r\n")
	// Drive letters shift as WinPE mounts volumes; locate ours by marker.
	fmt.Fprintf(&b, "for %%%%d in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do "+
		"if exist %%%%d:\\%s set DEVCELL_VOL=%%%%d:\r\n", AgentVolumeMarker)
	fmt.Fprintf(&b, "if \"!DEVCELL_VOL!\"==\"\" (ping -n %d 127.0.0.1 >nul & goto find)\r\n", poll+1)
	b.WriteString(":found\r\n")
	b.WriteString(progressLine(cfg, "agent-volume !DEVCELL_VOL!"))

	b.WriteString(":loop\r\n")
	// Setup's logs live on the X: RAM disk and die with the VM — copying
	// them out every poll is what makes a mid-install failure diagnosable
	// from the host (CELL-364).
	// Two source paths: early WinPE logs to X:\Windows\Panther, then
	// setup.exe switches to X:\$windows.~bt\Sources\Panther (run
	// 20260812T144140 snapshotted nothing copying only the early path).
	// The later path is copied second so it wins when both exist.
	fmt.Fprintf(&b, "copy /y X:\\Windows\\Panther\\setupact.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupActSnapshotName)
	fmt.Fprintf(&b, "copy /y X:\\Windows\\Panther\\setuperr.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupErrSnapshotName)
	fmt.Fprintf(&b, "copy /y X:\\$windows.~bt\\Sources\\Panther\\setupact.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupActSnapshotName)
	fmt.Fprintf(&b, "copy /y X:\\$windows.~bt\\Sources\\Panther\\setuperr.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupErrSnapshotName)
	fmt.Fprintf(&b, "if exist !DEVCELL_VOL!\\%s (\r\n", AgentCommandFile)
	fmt.Fprintf(&b, "  set /p DEVCELL_CMD=<!DEVCELL_VOL!\\%s\r\n", AgentCommandFile)
	// Delete first: a command that reboots or hangs must not re-run forever.
	fmt.Fprintf(&b, "  del /q !DEVCELL_VOL!\\%s\r\n", AgentCommandFile)
	fmt.Fprintf(&b, "  cmd /c !DEVCELL_CMD! >!DEVCELL_VOL!\\%s 2>&1\r\n", AgentResultFile)
	fmt.Fprintf(&b, "  echo done >!DEVCELL_VOL!\\%s\r\n", AgentDoneFile)
	b.WriteString("  " + progressLine(cfg, "ran !DEVCELL_CMD!"))
	b.WriteString(")\r\n")
	fmt.Fprintf(&b, "ping -n %d 127.0.0.1 >nul\r\n", poll+1)
	b.WriteString("goto loop\r\n")
	return []byte(b.String())
}

// WinPEEchoProbeScriptCommand returns the agent command that invokes the
// COM-port echo probe script.
func WinPEEchoProbeScriptCommand() string {
	return `%DEVCELL_VOL%\` + WinPEEchoProbeScriptName + ` %DEVCELL_VOL%`
}

// GenerateWinPEEchoProbeScript produces a WinPE script that:
//  1. Probes COM1 through COM4, echoing a unique marker to each port so the
//     host can determine which serial device maps to PCI-serial on ARM64.
//  2. Loads the viofs driver via drvload and mounts a virtiofs share using
//     virtiofs.exe, then writes a test file to the mount point.
//
// The answer volume path is passed as %1.
// viofs driver files and virtiofs.exe are expected under %1\drivers\viofs\.
// The virtiofs tag must match Spec.VirtioFSTag (default "devcell-logs").
func GenerateWinPEEchoProbeScript(viofsTag string) []byte {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("setlocal enabledelayedexpansion\r\n")
	b.WriteString("set VOL=%1\r\n")
	b.WriteString("\r\n")

	// Section 1: COM port probe
	b.WriteString("echo ===== COM PORT PROBE =====\r\n")
	for i := 1; i <= 4; i++ {
		marker := fmt.Sprintf("DEVCELL_COM_ECHO_COM%d", i)
		b.WriteString(fmt.Sprintf("echo %s >COM%d 2>nul\r\n", marker, i))
		b.WriteString(fmt.Sprintf("if errorlevel 1 (\r\n"))
		b.WriteString(fmt.Sprintf("  echo COM%d: FAILED\r\n", i))
		b.WriteString(fmt.Sprintf(") else (\r\n"))
		b.WriteString(fmt.Sprintf("  echo COM%d: OK\r\n", i))
		b.WriteString(fmt.Sprintf(")\r\n"))
	}
	b.WriteString("echo ===== COM PROBE DONE =====\r\n")
	b.WriteString("\r\n")

	// Section 2: viofs driver load + virtiofs mount
	b.WriteString("echo ===== VIOFS MOUNT =====\r\n")

	// Load the PnP driver
	b.WriteString("if exist !VOL!\\drivers\\viofs\\viofs.inf (\r\n")
	b.WriteString("  drvload !VOL!\\drivers\\viofs\\viofs.inf\r\n")
	b.WriteString("  echo drvload viofs: !errorlevel!\r\n")
	b.WriteString(") else (\r\n")
	b.WriteString("  echo viofs.inf not found — skipping driver load\r\n")
	b.WriteString(")\r\n")

	// Wait a moment for PnP to settle
	b.WriteString("ping -n 3 127.0.0.1 >nul\r\n")

	// Mount the virtiofs share
	mountLetter := "V:"
	b.WriteString("if exist !VOL!\\drivers\\viofs\\virtiofs.exe (\r\n")
	fmt.Fprintf(&b, "  !VOL!\\drivers\\viofs\\virtiofs.exe mount -t %s %s\r\n", viofsTag, mountLetter)
	b.WriteString("  if errorlevel 1 (\r\n")
	b.WriteString("    echo virtiofs mount: FAILED\r\n")
	b.WriteString("  ) else (\r\n")
	b.WriteString("    echo virtiofs mount: OK\r\n")
	fmt.Fprintf(&b, "    echo DEVCELL_VIOFS_HELLO >%s\\viofs-probe.txt\r\n", mountLetter)
	fmt.Fprintf(&b, "    if exist %s\\viofs-probe.txt (\r\n", mountLetter)
	b.WriteString("      echo viofs write: OK\r\n")
	b.WriteString("    ) else (\r\n")
	b.WriteString("      echo viofs write: FAILED\r\n")
	b.WriteString("    )\r\n")
	b.WriteString("  )\r\n")
	b.WriteString(") else (\r\n")
	b.WriteString("  echo virtiofs.exe not found — skipping mount\r\n")
	b.WriteString(")\r\n")
	b.WriteString("echo ===== VIOFS DONE =====\r\n")
	b.WriteString("\r\n")

	b.WriteString("echo DEVCELL ECHO PROBE COMPLETE\r\n")

	// go-diskfs v1.9.4 records the cluster-rounded size in the directory
	// entry instead of the actual file size, so reads return trailing
	// garbage for any file not cluster-aligned. Pad to a 512-byte boundary.
	if rem := b.Len() % 512; rem != 0 {
		b.WriteString("REM ")
		for b.Len()%512 != 0 {
			b.WriteByte('.')
		}
	}

	return []byte(b.String())
}

// progressLine emits an echo to the progress port, or nothing when no port is
// configured.
func progressLine(cfg WinPEPayloadConfig, msg string) string {
	if cfg.ProgressPort == "" {
		return ""
	}
	return fmt.Sprintf("echo devcell: %s >%s\r\n", msg, cfg.ProgressPort)
}
