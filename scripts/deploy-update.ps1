<#
.SYNOPSIS
    Stops the NSSM service, replaces the binary with a freshly built one,
    and restarts it. Intended to be called from the GitHub Actions
    self-hosted runner workflow, but safe to run manually too.
#>

param(
    [string]$ServiceName = "SILAPETBackend",
    [string]$InstallDir  = "E:\Server\Apps\office-craft-api\",
    [string]$ExeName     = "office-craft-api.exe",
    [string]$NewBinaryPath = "E:\Server\Apps\github-runner-silapet\_work\office-craft-api\office-craft-api\build\",
    [string]$NssmPath    = "E:\Server\Apps\NSSM\win64"
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

if (-not (Test-Path $NewBinaryPath)) {
    throw "New binary not found at $NewBinaryPath"
}

$nssm = Resolve-Nssm
$targetExe = Join-Path $InstallDir $ExeName

Write-Host "Stopping service '$ServiceName'..."
& $nssm stop $ServiceName
Start-Sleep -Seconds 2

Write-Host "Copying new binary into place..."
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Path $NewBinaryPath -Destination $targetExe -Force

Write-Host "Copying migrations..."
$migrationsSource = Join-Path (Split-Path $NewBinaryPath -Parent) "migrations"
if (Test-Path $migrationsSource) {
    Copy-Item -Path $migrationsSource -Destination $InstallDir -Recurse -Force
}

Write-Host "Starting service '$ServiceName'..."
& $nssm start $ServiceName

Start-Sleep -Seconds 2
& $nssm status $ServiceName

Write-Host "Deployment complete."
