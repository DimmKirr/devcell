package qemu

import (
	"fmt"

	"github.com/DimmKirr/devcell/internal/isokit"
)

// Guest-side diagnostics.
//
// Most of what goes wrong in a Windows cell is invisible from the host: the
// host can see that SSH does not answer, but not whether the NIC driver bound,
// which drive letter a device got, or whether a first-logon command failed.
// SSH would answer all of that, but SSH is usually the thing that is broken.
//
// So the guest writes its own report to the answer volume — removable, FAT,
// already attached, and readable from the host with ReadFileFromFAT. No
// network, no agent, and no keyboard automation, which under TCG proved
// unreliable: injected keystrokes auto-repeated and latched modifiers down.
const (
	// GuestDiagnosticsScriptName is the script placed on the answer volume.
	GuestDiagnosticsScriptName = "devcell-diag.ps1"
	// GuestDiagnosticsLogName is where that script writes its report, on the
	// same volume so the host can read it back.
	GuestDiagnosticsLogName = "devcell-diag.log"
)

// GenerateGuestDiagnosticsScript returns the PowerShell run at first logon.
//
// It locates its own volume by looking for the answer file rather than
// assuming a drive letter — Windows assigns those dynamically, and a wrong
// guess is exactly what made the NetKVM failure so hard to pin down.
func GenerateGuestDiagnosticsScript() []byte {
	return []byte(`# devcell guest diagnostics. Writes to the volume it was launched from,
# which the host reads back out of the raw FAT image.
$ErrorActionPreference = 'Continue'

$vol = $null
foreach ($d in (Get-Volume | Where-Object { $_.DriveLetter })) {
    if (Test-Path ("{0}:\autounattend.xml" -f $d.DriveLetter)) {
        $vol = "{0}:" -f $d.DriveLetter
        break
    }
}
if (-not $vol) { exit 1 }

Start-Transcript -Path "$vol\` + GuestDiagnosticsLogName + `" -Force

Write-Output "=== WHOAMI ==="
whoami

Write-Output "=== VOLUMES ==="
Get-Volume | Format-Table DriveLetter, FileSystemLabel, DriveType, Size -AutoSize | Out-String

Write-Output "=== NETWORK ADAPTERS ==="
# Empty here means the NetKVM driver never bound: no NIC, so no SSH.
Get-NetAdapter | Format-Table Name, Status, InterfaceDescription, LinkSpeed -AutoSize | Out-String

Write-Output "=== PROBLEM DEVICES ==="
Get-PnpDevice | Where-Object { $_.Status -ne 'OK' } |
    Format-Table Status, Class, FriendlyName -AutoSize | Out-String

Write-Output "=== IP ADDRESSES ==="
Get-NetIPAddress -AddressFamily IPv4 | Format-Table InterfaceAlias, IPAddress -AutoSize | Out-String

Write-Output "=== INTERNET REACHABLE ==="
# Add-WindowsCapability pulls OpenSSH from Windows Update, so this decides
# whether the SSH setup below could have worked at all.
(Test-NetConnection -ComputerName 8.8.8.8 -Port 53 -WarningAction SilentlyContinue).TcpTestSucceeded

Write-Output "=== OPENSSH CAPABILITY ==="
Get-WindowsCapability -Online -Name OpenSSH* | Format-Table Name, State -AutoSize | Out-String

Write-Output "=== SSHD SERVICE ==="
Get-Service sshd -ErrorAction SilentlyContinue | Format-Table Name, Status, StartType -AutoSize | Out-String

Write-Output "=== RDP ==="
"fDenyTSConnections = " + (Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -ErrorAction SilentlyContinue).fDenyTSConnections

Write-Output "=== WSL / OPTIONAL FEATURES ==="
Get-WindowsOptionalFeature -Online |
    Where-Object { $_.FeatureName -match 'Subsystem-Linux|VirtualMachinePlatform|Containers' } |
    Format-Table FeatureName, State -AutoSize | Out-String

Write-Output "=== FIREWALL ==="
Get-NetFirewallProfile | Format-Table Name, Enabled -AutoSize | Out-String

Stop-Transcript
`)
}

// ReadGuestDiagnostics reads the report the guest wrote to the answer volume.
// A missing log is an error rather than an empty string: it means the guest
// never got as far as running the script, which is itself the finding.
func ReadGuestDiagnostics(answerImagePath string) (string, error) {
	data, err := isokit.ReadFileFromFAT(answerImagePath, "/"+GuestDiagnosticsLogName)
	if err != nil {
		return "", fmt.Errorf("no guest diagnostics in %s — the guest never ran %s: %w",
			answerImagePath, GuestDiagnosticsScriptName, err)
	}
	return string(data), nil
}
