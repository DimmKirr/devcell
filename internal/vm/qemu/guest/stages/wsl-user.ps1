# Make the distro run as the cell's user.
#
# NixOS-WSL ships with "nixos", but every devcell engine runs the cell as the
# HOST's user, and the project share is linked at /home/<user>/dev/... Leaving
# the default makes the distro's user and the cell's paths disagree, and
# home-manager refuses to activate a config whose username does not match the
# invoking user.
#
# Procedure: https://nix-community.github.io/NixOS-WSL/how-to/change-username.html
# nixos-rebuild BOOT (not switch — the docs are explicit that switch
# misconfigures the account), then cycle the distro so the new generation's
# user takes effect.
param(
    [Parameter(Mandatory = $true)][string]$User,
    [string]$Distro  = 'NixOS',
    [string]$LogName = '004-devenv-WSL.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null
$env:WSL_UTF8 = '1'

$current = ''
Invoke-DevcellStep 'read the distro current user' {
    $script:current = (& wsl.exe -d $Distro -- /bin/sh -lc 'echo $USER' 2>&1 | Out-String).Trim()
    "distro user before: $script:current"
}

if ($current -eq $User) {
    Write-DevcellLog "distro already runs as $User"
    Write-DevcellLog 'DEVCELL-NO-CHANGE'
    return
}

Invoke-DevcellStep "declare $User in /etc/nixos" {
    $cfg = @"
{ config, lib, pkgs, ... }:
{
  wsl.defaultUser = "$User";
  users.users."$User" = {
    isNormalUser = true;
    home = "/home/$User";
    extraGroups = [ "wheel" ];
  };
  security.sudo.wheelNeedsPassword = false;
}
"@
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($cfg))
    wsl.exe -d $Distro -u root -- /bin/sh -lc "echo $b64 | base64 -d > /etc/nixos/devcell-user.nix"
    Assert-DevcellExitCode -What 'writing /etc/nixos/devcell-user.nix'
    wsl.exe -d $Distro -u root -- /bin/sh -lc 'grep -q devcell-user.nix /etc/nixos/configuration.nix || sed -i "s|imports = \[|imports = [ ./devcell-user.nix|" /etc/nixos/configuration.nix; grep -n imports /etc/nixos/configuration.nix'
    Assert-DevcellExitCode -What 'wiring devcell-user.nix into configuration.nix'
    "declared $User"
}

Invoke-DevcellStep 'nixos-rebuild boot' {
    # boot, NOT switch. This builds a generation inside the WSL2 VM under
    # double emulation — the slowest step of this stage.
    wsl.exe -d $Distro -u root -- /bin/sh -lc 'nixos-rebuild boot 2>&1 | tail -20'
    Assert-DevcellExitCode -What 'nixos-rebuild boot'
    'new generation built'
}

Invoke-DevcellStep 'cycle the distro so the new user takes effect' {
    wsl.exe --terminate $Distro
    wsl.exe -d $Distro --user root -- /bin/true
    wsl.exe --terminate $Distro
    'cycled'
}

Invoke-DevcellStep "verify the distro now runs as $User" {
    $after = (& wsl.exe -d $Distro -- /bin/sh -lc 'echo $USER; echo $HOME' 2>&1 | Out-String).Trim()
    if ($after -notmatch [regex]::Escape($User)) { throw "distro user is still not ${User}: $after" }
    "distro user after: $after"
}
