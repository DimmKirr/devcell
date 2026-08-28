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
    # The whole activation — nix-daemon ensure, nixhome extraction to ext4,
    # switch — runs as ONE wsl.exe call through the shipped helper. Split any
    # of it out and it breaks: WSL kills background processes per session, so
    # a daemon from an earlier call is dead (WSL#13236); and nix cannot read
    # the share directly — it ingests the repo as a dirty git tree and dies
    # on readlink for the share's symlinks (run 20260804). The nixhome
    # tarball ships on the control volume beside this script.
    $volLetter = (Split-Path (Split-Path $PSScriptRoot) -Qualifier) -replace ':$',''
    $volMount  = "/mnt/$($volLetter.ToLower())"
    wsl.exe -d $Distro -u root -- /bin/sh -c "mkdir -p $volMount; mountpoint -q $volMount || mount -t drvfs ${volLetter}: $volMount"
    Assert-DevcellExitCode -What "mounting the control volume at $volMount"
    $helper  = "$volMount/devcell/helpers/activate-home-manager.sh"
    $tarball = "$volMount/devcell/nixhome.tgz"
    (& wsl.exe -d $Distro -u root -- /bin/sh -c "sh '$helper' '$User' '$tarball'" 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'home-manager switch (via activation helper)'
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
