param(
    [Parameter(Mandatory)]
    [string]$InstallDir,

    [Parameter(Mandatory)]
    [string]$NssmPath,

    [int]$Port = 8080
)

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "========== Installing Office-Craft =========="
Write-Host ""

$scriptRoot = Split-Path $MyInvocation.MyCommand.Path -Parent

Write-Host "[1/2] Installing API Service..."

& "$scriptRoot\install-service.ps1" `
    -InstallDir $InstallDir `
    -NssmPath $NssmPath

if ($LASTEXITCODE -ne 0) {
    throw "Failed installing API service."
}

Write-Host ""

Write-Host "[2/2] Installing Tailscale Funnel Task..."

& "$scriptRoot\install-funnel-task.ps1" `
    -Port $Port

if ($LASTEXITCODE -ne 0) {
    throw "Failed installing Funnel task."
}

Write-Host ""
Write-Host "============================================="
Write-Host "Installation completed successfully."
Write-Host "============================================="