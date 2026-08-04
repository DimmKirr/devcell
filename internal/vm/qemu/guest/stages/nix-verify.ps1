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

Invoke-DevcellStep 'ensure systemd (and therefore nix-daemon) runs in the distro' {
    # NixOS is a MULTI-user store: every write goes through nix-daemon, which
    # systemd starts. WSL does not run systemd unless /etc/wsl.conf asks for
    # it, so without this the cell user's first build dies on
    #   error: opening lock file "/nix/var/nix/db/big-lock": Permission denied
    # (run 20260803T231223, both attempts).
    $has = (& wsl.exe -d $Distro -- /bin/sh -lc 'grep -qs "systemd[[:space:]]*=[[:space:]]*true" /etc/wsl.conf && echo yes || echo no' 2>&1 | Out-String).Trim()
    if ($has -match 'yes') {
        'systemd already enabled in /etc/wsl.conf'
    } else {
        wsl.exe -d $Distro -u root -- /bin/sh -lc 'printf "\n[boot]\nsystemd=true\n" >> /etc/wsl.conf'
        Assert-DevcellExitCode -What 'appending [boot] systemd=true to /etc/wsl.conf'
        # The setting is read when the distro starts, so it must be cycled.
        wsl.exe --terminate $Distro
        wsl.exe -d $Distro -- /bin/sh -lc 'true'
        Assert-DevcellExitCode -What 'restarting the distro with systemd'
        'systemd enabled; distro cycled'
    }
}

Invoke-DevcellStep 'wait for nix-daemon and prove the store is writable' {
    # WSL hardcodes /usr/bin/systemctl (microsoft/WSL#13236) — NixOS puts it
    # at /run/current-system/sw/bin/systemctl so systemd user sessions fail,
    # D-Bus never comes up, and socket-activated services like nix-daemon
    # never start (run 20260804T095500). Polling nix-store --add alone would
    # hang forever. After a few failed rounds we detect the stale socket and
    # start nix-daemon manually.
    #
    # The daemon must be started AND tested in the SAME wsl.exe call: WSL
    # kills background processes when the last session disconnects, so a
    # daemon started in one wsl.exe call is dead by the next (run 20260804).
    $deadline = (Get-Date).AddMinutes(10)
    $storePath = ''
    $attempt = 0
    $daemonStarted = $false
    do {
        $attempt++
        $out = (& wsl.exe -d $Distro -- /bin/sh -lc 'timeout 30 nix-store --add /etc/hostname 2>&1' 2>&1 | Out-String).Trim()
        $code = $LASTEXITCODE
        Write-DevcellLog "attempt $attempt (exit=$code): $out"
        if ($code -eq 0 -and $out -match '/nix/store/') {
            $storePath = ($out -split "`n" | Where-Object { $_ -match '^/nix/store/' } | Select-Object -First 1)
            break
        }
        # After 3 failed attempts, start nix-daemon manually and test in one
        # WSL session (daemon + su to the cell user for the store write).
        if ($attempt -ge 3 -and -not $daemonStarted) {
            Write-DevcellLog "nix-daemon not running after $attempt attempts — starting manually (WSL#13236 workaround)"
            $nixUser = (& wsl.exe -d $Distro -- /bin/sh -lc 'whoami' 2>&1 | Out-String).Trim() -replace '(?s).*\n',''
            if (-not $nixUser) { $nixUser = 'nixos' }
            # The helper ships on the control volume alongside this script.
            # WSL sees Windows drives at /mnt/<letter>/, so resolve the
            # volume letter and hand the path straight to sh.
            $helper = Join-Path $PSScriptRoot '..\helpers\start-nix-daemon.sh'
            $volLetter = (Split-Path (Split-Path $PSScriptRoot) -Qualifier) -replace ':$',''
            $wslHelper = "/mnt/$($volLetter.ToLower())/devcell/helpers/start-nix-daemon.sh"
            Write-DevcellLog "helper: $helper (WSL path: $wslHelper)"
            $combo = (& wsl.exe -d $Distro -u root -- /bin/sh -c "sh '$wslHelper' '$nixUser'" 2>&1 | Out-String).Trim()
            Write-DevcellLog "manual daemon + store test: $combo"
            $daemonStarted = $true
            if ($combo -match 'STORE_EXIT=0' -and $combo -match '/nix/store/') {
                $storePath = ($combo -split "`n" | Where-Object { $_ -match '^/nix/store/' } | Select-Object -First 1)
                break
            }
        }
        Start-Sleep -Seconds 20
    } while ((Get-Date) -lt $deadline)
    if (-not $storePath) {
        throw "nix-store --add never succeeded after $attempt attempts - nix-daemon may not be running or the socket is not accessible to the cell user"
    }
    "store writable after $attempt attempts: $storePath"
}

Invoke-DevcellStep 'enable flakes for the cell user' {
    wsl.exe -d $Distro -- /bin/sh -lc 'mkdir -p ~/.config/nix && printf "experimental-features = nix-command flakes\n" > ~/.config/nix/nix.conf'
    Assert-DevcellExitCode -What 'writing ~/.config/nix/nix.conf'
    'flakes enabled'
}

Invoke-DevcellStep 'report identity and versions' {
    (& wsl.exe -d $Distro -- /bin/sh -lc 'whoami; nix --version; nixos-version' 2>&1 | Out-String).Trim()
}
