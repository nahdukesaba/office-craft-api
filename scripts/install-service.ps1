<#
.SYNOPSIS
    Installs the Office-Craft API as a Windows service using NSSM.

.DESCRIPTION
    Run this once per machine (as Administrator) to register the service.
    Assumes nssm.exe is either on PATH or its folder is passed via -NssmPath.

.EXAMPLE
    .\install-service.ps1 -InstallDir "D:\Kerja\apps\office-craft" -NssmPath "D:\Kerja\dev\nssm-2.24\nssm-2.24\win64"
#>

param(
    [string]$ServiceName = "SILAPETBackend",
    [string]$InstallDir  = "E:\Server\Apps\office-craft-api",
    [string]$ExeName     = "office-craft-api.exe",
    [string]$NssmPath    = "E:\Server\Apps\NSSM\win64",
    [string]$EnvFile     = "$InstallDir\.env"
)

$ErrorActionPreference = "Stop"

function Resolve-Nssm {
    if ($NssmPath -ne "") {
        $candidate = Join-Path $NssmPath "nssm.exe"
        if (Test-Path $candidate) { return $candidate }
    }
    $onPath = Get-Command nssm.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    throw "nssm.exe not found. Install NSSM (https://nssm.cc/) and pass -NssmPath, or add it to PATH."
}

$nssm = Resolve-Nssm
$exePath = Join-Path $InstallDir $ExeName

if (-not (Test-Path $exePath)) {
    throw "Executable not found at $exePath. Build/copy the binary there first."
}
if (-not (Test-Path $EnvFile)) {
    Write-Warning "$EnvFile not found. The service will start but requests will fail until it exists."
}

Write-Host "Installing service '$ServiceName' -> $exePath"

& $nssm install $ServiceName $exePath
& $nssm set $ServiceName AppDirectory $InstallDir
& $nssm set $ServiceName AppStdout "$InstallDir\logs\stdout.log"
& $nssm set $ServiceName AppStderr "$InstallDir\logs\stderr.log"
& $nssm set $ServiceName AppRotateFiles 1
& $nssm set $ServiceName AppRotateBytes 10485760
& $nssm set $ServiceName Start SERVICE_AUTO_START
& $nssm set $ServiceName DisplayName "SILAPET API"
& $nssm set $ServiceName Description "Golang + Fiber backend for the SILAPET API Resource Management System"

New-Item -ItemType Directory -Force -Path "$InstallDir\logs" | Out-Null

Write-Host "Starting service..."
& $nssm start $ServiceName

Write-Host "Done. Check status with: nssm status $ServiceName"
