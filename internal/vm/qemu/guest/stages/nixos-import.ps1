# Import NixOS-WSL as a WSL2 distro, or prove the existing one boots.
#
# Ordering matters: check the registry FIRST. Run 20260803T075624 spent 84s
# on the GitHub releases API and a 577MB asset check before discovering the
# distro was already imported — pointless work and a needless network
# dependency on every resumed run.
param(
    [string]$Distro  = 'NixOS',
    [string]$LogName = '004-devenv-WSL.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null
$env:WSL_UTF8 = '1'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$registered = $false
Invoke-DevcellStep "check whether $Distro is already registered" {
    $list = (& wsl.exe --list --quiet 2>&1 | Out-String)
    $script:registered = ($list -match [regex]::Escape($Distro))
    if ($script:registered) { "$Distro already registered - no import needed" }
    else { "$Distro not registered - will import" }
}

if (-not $registered) {
    Invoke-DevcellStep 'set WSL default version to 2' {
        # NixOS-WSL does not support WSL1.
        wsl.exe --set-default-version 2
        Assert-DevcellExitCode -What 'wsl --set-default-version 2'
        'default version is 2'
    }

    $img = ''
    Invoke-DevcellStep 'fetch the NixOS-WSL release image' {
        $rel = Invoke-RestMethod -Uri 'https://api.github.com/repos/nix-community/NixOS-WSL/releases/latest' -UseBasicParsing
        # One image per architecture: the x86_64 nixos.wsl imports cleanly on
        # ARM64 and then every exec dies with ENOEXEC (errno 8).
        $assetName = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'nixos.aarch64.wsl' } else { 'nixos.wsl' }
        $asset = $rel.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
        if (-not $asset) { throw "no $assetName asset in the latest NixOS-WSL release" }
        $script:img = Join-Path $env:TEMP $asset.name
        if (-not (Test-Path $script:img)) {
            Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $script:img -UseBasicParsing
            "downloaded $($asset.name) from $($rel.tag_name)"
        } else {
            "using cached $($asset.name) ($((Get-Item $script:img).Length) bytes)"
        }
    }

    Invoke-DevcellStep "import $Distro as WSL2" {
        wsl.exe --install --from-file $script:img --no-launch
        if ($LASTEXITCODE -ne 0) {
            'wsl --install --from-file failed - falling back to wsl --import'
            New-Item -ItemType Directory -Path "C:\wsl\$Distro" -Force | Out-Null
            wsl.exe --import $Distro "C:\wsl\$Distro" $script:img --version 2
            Assert-DevcellExitCode -What "wsl --import $Distro"
        }
        "imported $Distro"
    }
}

if ($registered) {
    # Already imported (a resumed run from a checkpoint image). Booting the
    # utility VM just to print nixos-version costs ~7 minutes under TCG and
    # proves nothing the next stages do not: "verify nix in NixOS-WSL" runs
    # inside this distro and fails loudly if it is broken. Registration is
    # all this stage can add here.
    Invoke-DevcellStep "$Distro already present - list only" {
        (& wsl.exe --list --verbose 2>&1 | Out-String).Trim()
    }
    Write-DevcellLog 'DEVCELL-NO-CHANGE'
    return
}

Invoke-DevcellStep "prove the freshly imported $Distro boots" {
    (& wsl.exe --list --verbose 2>&1 | Out-String).Trim()
    $version = (& wsl.exe -d $Distro -- nixos-version 2>&1 | Out-String).Trim()
    Assert-DevcellExitCode -What "nixos-version inside $Distro"
    "nixos-version: $version"
}
