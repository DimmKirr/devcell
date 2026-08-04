# Activate the repo's nixhome profile inside NixOS-WSL.
#
# The last link in the chain: the project share becomes visible inside the
# distro, gets linked to the agreed repo path, and home-manager activates
# from it. Everything before this stage exists to make this possible.
#
# $User is the WSL DISTRO user (WSLDistroUser), NOT the Windows session user.
# home-manager refuses to activate a config whose username differs from the
# invoking user — `Error: USER is set to "X" but we expect "Y"` — and the
# nixhome wsl-* configs are built for the distro's own default account.
param(
    [string]$User    = 'nixos',
    [string]$Drive   = 'Z:',
    [string]$Mount   = '/mnt/z',
    [string]$Distro  = 'NixOS',
    [string]$LogName = '005-devenv-home-manager.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null
$env:WSL_UTF8 = '1'

$repo = "/home/$User/dev/dimmkirr/devcell"

Invoke-DevcellStep "mount the project share at $Mount" {
    # WSL2 usually automounts Windows drives under /mnt, but not always, and
    # the share is this stage's only external dependency. `mountpoint -q`
    # short-circuits when it is already mounted, so a non-zero exit here means
    # the mount genuinely failed — which must be REPORTED. The previous
    # version discarded it with `2>/dev/null; true`, so a missing share
    # surfaced ~30 minutes later as an unexplained `ls` error instead.
    wsl.exe -d $Distro -u root -- /bin/sh -c "mkdir -p $Mount; mountpoint -q $Mount || mount -t drvfs $Drive $Mount"
    Assert-DevcellExitCode -What "mounting $Drive at $Mount"
    "share mounted at $Mount"
}

Invoke-DevcellStep "link the repo at $repo" {
    # chown on a drvfs symlink is best-effort — the Windows filesystem has no
    # POSIX owner to set — so only the link itself is asserted.
    wsl.exe -d $Distro -u root -- /bin/sh -c "mkdir -p /home/$User/dev/dimmkirr && ln -sfn $Mount $repo"
    Assert-DevcellExitCode -What "linking $Mount to $repo"
    wsl.exe -d $Distro -u root -- /bin/sh -c "chown -h ${User}: $repo 2>/dev/null; true"
    "linked $Mount -> $repo"
}

Invoke-DevcellStep 'prove the repo is readable through the share' {
    (& wsl.exe -d $Distro -- /bin/sh -c "ls $repo/nixhome" 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'reading nixhome through the share'
}

Invoke-DevcellStep 'activate nixhome via home-manager' {
    # The official standalone-flake activation
    # (nix-community.github.io/home-manager/installation.html), with three
    # details that each cost a multi-hour run:
    #  - LOGIN shell (-lc): NixOS-WSL puts nix on PATH via /etc/profile only,
    #    so a bare `wsl -- nix` is exit 127 on a working distro.
    #  - the experimental features travel as REPEATED flags, never as one
    #    quoted pair: inner double quotes do not survive the
    #    PowerShell -> wsl.exe -> sh -lc chain and nix then reports "no
    #    subcommand specified" (run 20260802T112212). Relying on an ambient
    #    nix.conf failed earlier too (run 20260802T095306).
    #  - the runner is pinned to the home-manager branch matching this NixOS.
    #  - the output is truncated through a FILE, never a pipe: `... | tail -40`
    #    makes $? the status of tail, which always succeeds, so a failed
    #    activation reported "ok" and only surfaced 36s later as
    #    `home-manager --version` exit 127 (run 20260803T231223). /bin/sh here
    #    is not guaranteed to support pipefail, so capture rc explicitly.
    $activate = 'set -e; cd ' + $repo + '; ' +
        'ARCH_SUFFIX=$([ "$(uname -m)" = "aarch64" ] && echo "-aarch64" || echo ""); ' +
        'nix --extra-experimental-features nix-command --extra-experimental-features flakes ' +
        'run home-manager/release-26.05 -- switch -b backup ' +
        '--flake ./nixhome#wsl-base$ARCH_SUFFIX > /tmp/devcell-hm-activate.log 2>&1; ' +
        'rc=$?; tail -40 /tmp/devcell-hm-activate.log; exit $rc'
    (& wsl.exe -d $Distro -- /bin/sh -lc $activate 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'home-manager switch'
}

Invoke-DevcellStep 'prove home-manager is on the activated profile' {
    # Installed means the CLI answers with a real version from the activated
    # profile (programs.home-manager.enable in nixhome), not merely exit 0.
    $v = (& wsl.exe -d $Distro -- /bin/sh -lc 'home-manager --version' 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'home-manager --version'
    if ($v -notmatch '[0-9]+\.[0-9]+') {
        throw "home-manager --version did not print a semantic version: $v"
    }
    "home-manager $v"
}
