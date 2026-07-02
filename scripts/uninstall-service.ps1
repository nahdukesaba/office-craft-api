<#
.SYNOPSIS
    Stops and removes the Office-Craft Windows service registered via NSSM.
#>

param(
    [string]$ServiceName = "OfficeCraftBackend",
    [string]$NssmPath    = ""
)

$ErrorActionPreference = "Stop"

function Resolve-Nssm {
    if ($NssmPath -ne "") {
        $candidate = Join-Path $NssmPath "nssm.exe"
        if (Test-Path $candidate) { return $candidate }
    }
    $onPath = Get-Command nssm.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    throw "nssm.exe not found. Pass -NssmPath or add it to PATH."
}

$nssm = Resolve-Nssm

Write-Host "Stopping service '$ServiceName'..."
& $nssm stop $ServiceName

Write-Host "Removing service '$ServiceName'..."
& $nssm remove $ServiceName confirm

Write-Host "Done."
