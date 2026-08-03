# Devcell.psm1 — the shared guest library.
#
# Delivered fresh on the per-run control volume (never baked into a qcow2, so
# a checkpoint image cannot freeze a stale copy). Every stage script imports
# this module; nothing here is generated, interpolated, or templated — it is
# real PowerShell, lintable and runnable standalone on a guest.

Set-StrictMode -Version Latest
# Progress records travel over SSH as CLIXML and once turned two stage logs
# into 8.9MB and 11.8MB of noise; the run at 20260803T083705 showed them
# again from Get-Volume.
$ProgressPreference = 'SilentlyContinue'

# Get-DevcellControlVolume returns the drive letter of the control volume,
# found by its marker file. The letter is assigned by Windows and moves
# between boots (observed D: and E:), so it must never be hardcoded.
function Get-DevcellControlVolume {
    param([string]$Marker = 'devcell-guest-logs.txt')
    $vol = (Get-Volume |
        Where-Object DriveLetter |
        Where-Object { Test-Path ($_.DriveLetter + ':\' + $Marker) } |
        Select-Object -First 1).DriveLetter
    return $vol
}

# Write-DevcellLog appends a timestamped line to BOTH the SSH stream and the
# control volume. Add-Content flushes per call, so a long stage is readable
# while it runs — proven 20260803T073911, where the host saw a line 21s
# before the stage ended. Start-Transcript alone buffers and reveals nothing.
function Write-DevcellLog {
    param(
        [Parameter(Mandatory = $true, ValueFromPipeline = $true)][string]$Message,
        [string]$LogFile = $script:DevcellLogFile
    )
    process {
        $line = ((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ') + ' ' + $Message)
        Write-Output $line
        if ($LogFile) {
            try { Add-Content -Path $LogFile -Value $line -Encoding utf8 -ErrorAction Stop }
            catch { Write-Output ('[log] volume write failed: ' + $_.Exception.Message) }
        }
    }
}

# Invoke-DevcellStep runs a labelled unit of work, timing it and reporting
# the outcome. Without it a 20-minute operation logs nothing until it ends,
# which is how a timeout became indistinguishable from a hang.
function Invoke-DevcellStep {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    Write-DevcellLog ('step start: ' + $Label)
    $sw = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $Body 2>&1 | ForEach-Object { Write-DevcellLog ('  ' + $_) }
        Write-DevcellLog ('step ok: ' + $Label + ' in ' + [int]$sw.Elapsed.TotalSeconds + 's')
    } catch {
        Write-DevcellLog ('step FAILED: ' + $Label + ' after ' + [int]$sw.Elapsed.TotalSeconds + 's: ' + $_.Exception.Message)
        throw
    }
}

# Assert-DevcellExitCode fails a stage on a native command's exit code.
# Native tools (wsl.exe, pnputil, msiexec) do not throw; three stages once
# reported success over a failed command because only $LASTEXITCODE knew.
function Assert-DevcellExitCode {
    param([Parameter(Mandatory = $true)][string]$What, [int]$Code = $global:LASTEXITCODE)
    if ($Code -ne 0) { throw ($What + ' failed with exit code ' + $Code) }
}

# Initialize-DevcellLogging resolves the control volume once, loudly, and
# points Write-DevcellLog at this stage's component log. A missing volume is
# reported, never swallowed — silent catch{} is why volume logs were empty
# for two days with no explanation.
function Initialize-DevcellLogging {
    param([Parameter(Mandatory = $true)][string]$LogName, [string]$Marker = 'devcell-guest-logs.txt')
    $script:DevcellLogVol = Get-DevcellControlVolume -Marker $Marker
    if ($script:DevcellLogVol) {
        $script:DevcellLogFile = ($script:DevcellLogVol + ':\' + $LogName)
        Write-Output ('[log] volume ' + $script:DevcellLogVol + ': -> ' + $script:DevcellLogFile)
    } else {
        $script:DevcellLogFile = $null
        Write-Output '[log] CONTROL VOLUME NOT FOUND - guest-side logs will not reach the host'
    }
    return $script:DevcellLogVol
}

Export-ModuleMember -Function Write-DevcellLog, Invoke-DevcellStep,
    Get-DevcellControlVolume, Assert-DevcellExitCode, Initialize-DevcellLogging
