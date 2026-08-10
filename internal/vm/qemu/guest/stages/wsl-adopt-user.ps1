# Make the distro run as the CELL's user, after home-manager has activated.
#
# Every devcell engine presents the host's user inside the cell. Docker does it
# in the entrypoint: the nix profile is built for a fixed user (`devcell`) and
# the session user is created at runtime with the dotfiles rewritten onto its
# home. WSL had no equivalent step, so `whoami` inside the distro answered
# "nixos" (run 20260803T231223).
#
# Why this runs AFTER activation, never before:
#   home-manager's activation script ends in `checkUsername <name>`, where
#   <name> is baked in from home.username at build time. nixhome pins
#   wslUser.username = "nixos" (nixhome/flake.nix), so activating as anyone
#   else fails with
#       Error: USER is set to "dmitry" but we expect "nixos"
#   Activation is a ONE-TIME build step, though: afterwards the result is
#   store paths plus symlinks under /home/nixos, and the guard never runs
#   again — so the host user can be introduced safely once it is done.
#
# Procedure: https://nix-community.github.io/NixOS-WSL/how-to/change-username.html
# nixos-rebuild BOOT (not switch — the docs are explicit that switch
# misconfigures the account), then cycle the distro.
param(
    [Parameter(Mandatory = $true)][string]$User,
    [string]$From    = 'nixos',
    [string]$Distro  = 'NixOS',
    [string]$LogName = '005-devenv-home-manager.log'
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
  # mkForce: NixOS-WSL's stock configuration.nix pins wsl.defaultUser =
  # "nixos" at normal priority; without force the rebuild dies on
  # "conflicting definition values" (run 20260804).
  wsl.defaultUser = lib.mkForce "$User";
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
    # boot, NOT switch — via the shipped helper, which ensures the nix-daemon
    # in the SAME wsl.exe session (WSL#13236: no earlier stage's daemon
    # survives) and runs the rebuild with NIX_REMOTE=daemon: root's local
    # store mode dies on temproots ENOENT in this image while every
    # daemon-mediated build works (3 failed + 1 green run, 20260805).
    $volLetter = (Split-Path (Split-Path $PSScriptRoot) -Qualifier) -replace ':$',''
    $volMount  = "/mnt/$($volLetter.ToLower())"
    wsl.exe -d $Distro -u root -- /bin/sh -c "mkdir -p $volMount; mountpoint -q $volMount || mount -t drvfs ${volLetter}: $volMount"
    Assert-DevcellExitCode -What "mounting the control volume at $volMount"
    $helper = "$volMount/devcell/helpers/nixos-rebuild-boot.sh"
    (& wsl.exe -d $Distro -u root -- /bin/sh -c "sh '$helper' '$From'" 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'nixos-rebuild boot (via rebuild helper)'
    'new generation built'
}

Invoke-DevcellStep "carry the activated profile into /home/$User" {
    # The home-manager result is symlinks into /nix/store, which are absolute
    # — so copying them preserves a working environment. `cp -a` keeps them as
    # links rather than dereferencing gigabytes of store closure.
    # No chown yet: the user does not exist until the new generation boots,
    # so a chown here dies with "invalid spec" (run 20260805).
    wsl.exe -d $Distro -u root -- /bin/sh -lc "mkdir -p /home/$User && cp -a /home/$From/. /home/$User/"
    Assert-DevcellExitCode -What "copying /home/$From to /home/$User"
    "profile carried from $From"
}

Invoke-DevcellStep 'cycle the distro so the new user takes effect' {
    wsl.exe --terminate $Distro
    wsl.exe -d $Distro --user root -- /bin/sh -lc 'true'
    wsl.exe --terminate $Distro
    'cycled'
}

Invoke-DevcellStep "own /home/$User now the user exists" {
    # Only the booted generation carries the account — chown works here and
    # nowhere earlier.
    wsl.exe -d $Distro -u root -- /bin/sh -lc "chown -R ${User}: /home/$User"
    Assert-DevcellExitCode -What "chown /home/$User"
    "home owned by $User"
}

Invoke-DevcellStep "verify the distro runs as $User with a working nix env" {
    # Identity AND environment: a rename that leaves the cell without nix is
    # not a success. home-manager's own CLI is the profile's canary.
    $after = (& wsl.exe -d $Distro -- /bin/sh -lc 'whoami; echo "HOME=$HOME"; nix --version; home-manager --version' 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What "nix env for $User"
    if ($after -notmatch "^$([regex]::Escape($User))") {
        throw "distro user is still not ${User}: $after"
    }
    "distro identity after: $after"
}
