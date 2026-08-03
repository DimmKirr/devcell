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

// WinPEPayloadConfig parameterises the generated WinPE payload scripts.
type WinPEPayloadConfig struct {
	// DriverINFs are loaded with drvload before setup.exe starts. Usually
	// empty: NVMe and USB storage have inbox Windows ARM64 drivers, so
	// injection is only needed for extras like virtio-net.
	DriverINFs []string
	// ProgressPort is a guest COM port (e.g. "COM1") that progress lines are
	// echoed to. Pair with Spec.GuestProgressLogPath so the host can read it.
	ProgressPort string
	// PollSeconds is how often the agent checks for a new command (default 5).
	PollSeconds int
}

// GenerateWinPEShellINI produces winpeshl.ini, which replaces WinPE's default
// startup. Entries run in order and synchronously, so the bootstrap is listed
// first and setup.exe second — dropping setup.exe here would leave WinPE with
// nothing to do after the bootstrap returns.
func GenerateWinPEShellINI() []byte {
	return []byte("[LaunchApps]\n" +
		WinPEBootstrapPath + "\n" +
		`%SYSTEMDRIVE%\setup.exe` + "\n")
}

// GenerateWinPEBootstrap produces the script that runs before setup.exe:
// loads any requested drivers, starts the agent detached, and reports
// progress to the host.
func GenerateWinPEBootstrap(cfg WinPEPayloadConfig) []byte {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString(progressLine(cfg, "bootstrap-start"))

	for _, inf := range cfg.DriverINFs {
		fmt.Fprintf(&b, "drvload %s\r\n", inf)
		b.WriteString(progressLine(cfg, "drvload "+inf))
	}

	// winpeshl runs entries synchronously; without `start` the agent's poll
	// loop would never return and setup.exe would never launch.
	fmt.Fprintf(&b, "start \"devcell-agent\" /min cmd /c %s\r\n", WinPEAgentPath)
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
	fmt.Fprintf(&b, "if \"!DEVCELL_VOL!\"==\"\" (timeout /t %d /nobreak >nul & goto find)\r\n", poll)
	b.WriteString(":found\r\n")
	b.WriteString(progressLine(cfg, "agent-volume !DEVCELL_VOL!"))

	b.WriteString(":loop\r\n")
	// Setup's logs live on the X: RAM disk and die with the VM — copying
	// them out every poll is what makes a mid-install failure diagnosable
	// from the host (CELL-364).
	fmt.Fprintf(&b, "copy /y X:\\Windows\\Panther\\setupact.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupActSnapshotName)
	fmt.Fprintf(&b, "copy /y X:\\Windows\\Panther\\setuperr.log !DEVCELL_VOL!\\%s >nul 2>&1\r\n", SetupErrSnapshotName)
	fmt.Fprintf(&b, "if exist !DEVCELL_VOL!\\%s (\r\n", AgentCommandFile)
	fmt.Fprintf(&b, "  set /p DEVCELL_CMD=<!DEVCELL_VOL!\\%s\r\n", AgentCommandFile)
	// Delete first: a command that reboots or hangs must not re-run forever.
	fmt.Fprintf(&b, "  del /q !DEVCELL_VOL!\\%s\r\n", AgentCommandFile)
	fmt.Fprintf(&b, "  cmd /c !DEVCELL_CMD! >!DEVCELL_VOL!\\%s 2>&1\r\n", AgentResultFile)
	b.WriteString("  " + progressLine(cfg, "ran !DEVCELL_CMD!"))
	b.WriteString(")\r\n")
	fmt.Fprintf(&b, "timeout /t %d /nobreak >nul\r\n", poll)
	b.WriteString("goto loop\r\n")
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
