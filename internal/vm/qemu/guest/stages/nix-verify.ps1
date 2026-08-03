# Prove the toolchain the NixOS-WSL image already carries.
#
# NixOS *is* nix — running the upstream installer inside it would be both
# redundant and non-idiomatic. Every nix call goes through a LOGIN shell:
# NixOS-WSL sets nix's PATH in /etc/profile only, so a bare `wsl -- nix` is
# exit 127 on a perfectly working distro (run 20260802).
param(
    [string]$Distro  = 'NixOS',
    [string]$LogName = '004-devenv-WSL.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null
$env:WSL_UTF8 = '1'

Invoke-DevcellStep 'record the guest environment' {
    # USER/HOME/PATH decide where home-manager activates and whether its CLI
    # is reachable; the Windows-interop entries are what let the cell call
    # Windows tools. Recording beats interrogating a busy guest later.
    (& wsl.exe -d $Distro -- /bin/sh -lc 'echo "guest USER=$USER"; echo "guest HOME=$HOME"; echo "guest SHELL=$SHELL"; echo "guest PATH=$PATH"' 2>&1 | Out-String).Trim()
}

Invoke-DevcellStep 'nix answers inside the distro' {
    $v = (& wsl.exe -d $Distro -- /bin/sh -lc 'nix --version' 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What 'nix --version'
    $v
}

Invoke-DevcellStep 'enable flakes for the cell user' {
    wsl.exe -d $Distro -- /bin/sh -lc 'mkdir -p ~/.config/nix && printf "experimental-features = nix-command flakes\n" > ~/.config/nix/nix.conf'
    Assert-DevcellExitCode -What 'writing ~/.config/nix/nix.conf'
    'flakes enabled'
}

Invoke-DevcellStep 'report identity and versions' {
    (& wsl.exe -d $Distro -- /bin/sh -lc 'whoami; nix --version; nixos-version' 2>&1 | Out-String).Trim()
}
