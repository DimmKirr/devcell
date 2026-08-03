# Enable the two Windows features WSL2 requires.
#
# NixOS-WSL does not support WSL1 (https://nix-community.github.io/NixOS-WSL/install.html),
# so VirtualMachinePlatform is required, not optional. The reboot belongs to
# the caller, which watches SSH drop and come back.
param(
    [string]$LogName = '001-devenv-WSL.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null

$changed = $false
Invoke-DevcellStep 'enable WSL2 features' {
    foreach ($f in @('Microsoft-Windows-Subsystem-Linux', 'VirtualMachinePlatform')) {
        $before = (Get-WindowsOptionalFeature -Online -FeatureName $f).State
        if ($before -eq 'Enabled') { "$f already enabled"; continue }
        $r = Enable-WindowsOptionalFeature -Online -FeatureName $f -All -NoRestart
        $script:changed = $true
        "$f enabled, restart needed: $($r.RestartNeeded)"
    }
    foreach ($f in @('Microsoft-Windows-Subsystem-Linux', 'VirtualMachinePlatform')) {
        "state ${f}: $((Get-WindowsOptionalFeature -Online -FeatureName $f).State)"
    }
}

# Both features already on: nothing to activate, so skip the ~8min TCG reboot.
if (-not $changed) { Write-DevcellLog 'DEVCELL-NO-CHANGE' }
