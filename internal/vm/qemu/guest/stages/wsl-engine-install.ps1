# Install the WSL engine MSI and tune WSL for an emulated host.
#
# The inbox wsl.exe on current Win11 is a stub; the engine ships as a
# separate MSI from the microsoft/WSL releases. Installing it tears down the
# SSH session, so the stage runs disconnect-tolerant and reboot-terminated.
param(
    [string]$LogName = '004-devenv-WSL.log'
)
Import-Module (Join-Path $PSScriptRoot '..\Devcell.psm1') -Force
Initialize-DevcellLogging -LogName $LogName | Out-Null
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$env:WSL_UTF8 = '1'

Invoke-DevcellStep 'write .wslconfig for an emulated host' {
    # WSL's defaults assume real hardware. Under TCG the utility-VM kernel
    # needs far more than the default 30s KernelBootTimeout (WslCoreConfig.h)
    # — 15 min was still short on a loaded host — and WSLg's vGPU has no
    # partitionable GPU here; its hot-add was the last HCS operation before
    # wslservice died with E_UNEXPECTED.
    $cfg = @(
        '[wsl2]', 'processors=2', 'memory=2GB',
        'kernelBootTimeout=3600000', 'distributionStartTimeout=3600000',
        'gpuSupport=false', 'guiApplications=false'
    ) -join "`n"
    Set-Content -Path (Join-Path $env:USERPROFILE '.wslconfig') -Value $cfg -Encoding ascii
    'wrote ' + (Join-Path $env:USERPROFILE '.wslconfig')
}

$status = ''
Invoke-DevcellStep 'probe the WSL engine' {
    $script:status = (& wsl.exe --status 2>&1 | Out-String)
    'wsl --status exit ' + $LASTEXITCODE
    $script:status.Trim()
}

if ($script:status -notmatch 'not installed') {
    Write-DevcellLog 'wsl engine already present'
    # Tell the runner nothing changed: the reboot this stage declares exists
    # for the MSI install path, and a TCG reboot costs ~8 minutes.
    Write-DevcellLog 'DEVCELL-NO-CHANGE'
    return
}

Invoke-DevcellStep 'register the engine (wsl --install --no-distribution)' {
    wsl.exe --install --no-distribution
    'exit ' + $LASTEXITCODE
}

Invoke-DevcellStep 'install the engine MSI if registration did not take' {
    $probe = (& wsl.exe --status 2>&1 | Out-String)
    if ($probe -notmatch 'not installed') { return 'engine registered' }
    $rel = Invoke-RestMethod -Uri 'https://api.github.com/repos/microsoft/WSL/releases/latest' -UseBasicParsing
    $asset = $rel.assets | Where-Object { $_.name -like '*arm64.msi' } | Select-Object -First 1
    if (-not $asset) { throw 'no arm64.msi asset in the latest microsoft/WSL release' }
    $msi = Join-Path $env:TEMP $asset.name
    if (-not (Test-Path $msi)) { Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $msi -UseBasicParsing }
    "installing $($asset.name) - the SSH session may drop here"
    $p = Start-Process msiexec -ArgumentList '/i', $msi, '/qn', '/norestart' -Wait -PassThru
    Assert-DevcellExitCode -What ('msiexec ' + $asset.name) -Code $p.ExitCode
    'installed ' + $asset.name
}
