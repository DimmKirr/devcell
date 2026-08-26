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

// The keep-alive probe is what a troubleshooting session starts from: it
// proves both channels into the guest before anything is debugged through
// them, so a silent guest is never mistaken for a broken change.

func TestGenerateKeepAliveScript_ProvesBothChannels(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	// The file the host placed on the FAT volume, echoed back verbatim.
	assert.Contains(t, script, KeepAliveProbeFile)
	assert.Contains(t, script, "PROBE_FILE=")

	// Proof the agent shell itself ran.
	assert.Contains(t, script, "PROBE_SHELL=OK")

	// Enough context to debug from: where we are and what is loaded.
	assert.Contains(t, script, "HYPERVISOR_PRESENT=")

	assert.Contains(t, script, KeepAliveBanner)

	for i, line := range strings.Split(script, "\n") {
		if line == "" {
			continue
		}
		assert.True(t, strings.HasSuffix(line, "\r"), "line %d lacks CR: %q", i+1, line)
	}
}

func TestKeepAliveProbeCommand_RunsTheScript(t *testing.T) {
	assert.Contains(t, KeepAliveProbeCommand(), KeepAliveScriptName)
}

func TestGenerateKeepAliveScript_ParsesAsPowerShell(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed; skipping syntax validation")
	}
	scriptPath := filepath.Join(t.TempDir(), KeepAliveScriptName)
	require.NoError(t, os.WriteFile(scriptPath, GenerateKeepAliveScript(), 0644))

	check := `$errs = $null
[System.Management.Automation.Language.Parser]::ParseFile('` + scriptPath + `', [ref]$null, [ref]$errs) | Out-Null
if ($errs) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 }
Write-Output 'SYNTAX_OK'`

	out, err := exec.Command("pwsh", "-NoProfile", "-Command", check).CombinedOutput()
	require.NoError(t, err, "keepalive script has syntax errors:\n%s", out)
	assert.Contains(t, string(out), "SYNTAX_OK")
}

// SSH turns the keep-alive VM from a screenshot-and-keystrokes puppet into a
// real shell. The server is this repo's own gosshd, cross-compiled for
// windows/arm64 and staged on the FAT volume; the QEMU argv forwards a host
// port to guest :22. WinPE has no service-control CLI, so it runs as a plain
// background process rather than a service.
func TestGenerateKeepAliveScript_StartsGoSSHD(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	// WinPE boots with the network stack down.
	assert.Contains(t, script, "wpeutil")
	assert.Contains(t, script, "InitializeNetwork")

	// The payload the host stages on the FAT volume.
	assert.Contains(t, script, GoSSHDPayloadName)

	// No sc.exe in WinPE: the server runs detached, not as a service.
	assert.Contains(t, script, "Start-Process")

	// Markers the host asserts on.
	assert.Contains(t, script, "GOSSHD_PROC=")
	assert.Contains(t, script, "GUEST_IP=")
}

// Win32-OpenSSH cannot serve a session in WinPE: per connection it spawns a
// pre-auth child as an LSA virtual account and authenticates the user with
// an S4U logon, and WinPE's minimal lsass provides neither, so the connection
// closes before authentication. Privilege separation has been mandatory
// upstream since 7.5, so no sshd_config avoids it. Anything reintroducing
// Windows-account auth here would fail the same way, silently.
func TestGenerateKeepAliveScript_NeedsNoWindowsAccountMachinery(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	// "sshd.exe" itself is not checkable here: it is a substring of the
	// payload's own name, devcell-gosshd.exe. These tokens are unambiguous.
	for _, absent := range []string{
		"ssh-keygen",
		"sshd_config",
		"authorized_keys",
		"Expand-Archive",
	} {
		assert.NotContains(t, script, absent,
			"%s belongs to Win32-OpenSSH, whose auth cannot work in WinPE", absent)
	}
}

// WinPE ships no Storage module, so Get-Volume is a command-not-found —
// terminating even under ErrorActionPreference='Continue'. It aborted the
// probe before sshd ever started (run #6, 2026-08-23).
func TestGenerateKeepAliveScript_AvoidsCmdletsAbsentFromWinPE(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	for _, absent := range []string{"Get-Volume", "Get-Disk", "Get-Partition"} {
		assert.NotContains(t, script, absent,
			"%s is not in WinPE; calling it aborts the script", absent)
	}
	assert.Contains(t, script, "Get-PSDrive",
		"volume listing must use a cmdlet core PowerShell always provides")
}

// WinPE's firewall drops inbound connections even when the server is listening:
// QEMU's SLIRP showed TCP[SYN_SENT] to 10.0.2.15:22 with no reply while
// sshd had 0.0.0.0:22 bound (run #11, 2026-08-23). A listening socket is
// not reachability.
func TestGenerateKeepAliveScript_OpensFirewallForSSH(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	// wpeutil is the only firewall control WinPE ships. The advfirewall
	// context needs mpssvc, which is absent, so both `add rule` and
	// `set allprofiles state off` return exit 1 and the block stays.
	assert.Contains(t, script, "wpeutil DisableFirewall")
	assert.NotContains(t, script, "advfirewall")
	assert.Contains(t, script, "FIREWALL_SSH=")

	// The firewall must be down before the server starts, so the first
	// connection attempt is not the one that discovers the block.
	assert.Less(t, strings.Index(script, "DisableFirewall"), strings.Index(script, "Start-Process -FilePath $sshExe"),
		"the firewall must be disabled before the ssh server starts")
}

// The server's own log is the only thing that names why a session failed, and
// the guest's RAM disk dies with the VM. Writing it to the shared volume is
// what makes it readable from the host afterwards; without this a failed
// session reports only "Connection closed" with no reason on either side.
func TestGenerateKeepAliveScript_LogsGoSSHDToSharedVolume(t *testing.T) {
	script := string(GenerateKeepAliveScript())

	assert.Contains(t, script, GoSSHDLogFile,
		"the server log must land on the shared volume so the host can read it")

	// Reading the log before a connection can arrive is what made the first
	// failing run undiagnosable, so the path must be handed over at start.
	assert.Less(t, strings.Index(script, GoSSHDLogFile), strings.Index(script, "GOSSHD_PROC="),
		"the log path must be passed when the server starts, not after")
}
