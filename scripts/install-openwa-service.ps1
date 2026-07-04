<#
.SYNOPSIS
    Installs OpenWA (WhatsApp gateway) as a Windows service using NSSM.

.DESCRIPTION
    Mirrors scripts/install-service.ps1 from the Office-Craft backend. Run
    once per machine (as Administrator) after you've built OpenWA
    (`npm run build && npm run dashboard:build`) and already linked your
    WhatsApp number via the foreground run (see WHATSAPP_SETUP.md step 5).

.EXAMPLE
    .\install-openwa-service.ps1 -InstallDir "C:\apps\OpenWA" -NssmPath "C:\tools\nssm\win64"
#>

param(
    [string]$ServiceName = "OpenWABackend",
    [string]$InstallDir  = "C:\apps\OpenWA",
    [string]$NodeExe     = "",
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
    throw "nssm.exe not found. Install NSSM (https://nssm.cc/) and pass -NssmPath, or add it to PATH."
}

function Resolve-Node {
    if ($NodeExe -ne "") {
        # Be forgiving if someone points -NodeExe at the nodejs *folder*
        # instead of node.exe itself - append it rather than silently
        # trying to launch a directory as a process (which fails with an
        # opaque "service-specific error code 3" and zero log output).
        if ((Test-Path $NodeExe -PathType Container)) {
            $NodeExe = Join-Path $NodeExe "node.exe"
        }
        if (Test-Path $NodeExe -PathType Leaf) { return (Resolve-Path $NodeExe).Path }
        throw "NodeExe path '$NodeExe' is not a file. Pass the full path to node.exe, e.g. -NodeExe 'C:\Program Files\nodejs\node.exe'."
    }
    $onPath = Get-Command node.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    throw "node.exe not found. Install Node.js LTS, or pass -NodeExe with the full path to node.exe."
}

$nssm = Resolve-Nssm
$node = Resolve-Node
$entryPoint = Join-Path $InstallDir "dist\main.js"

if (-not (Test-Path $entryPoint)) {
    throw "Built entry point not found at $entryPoint. Run 'npm run build' inside $InstallDir first."
}
if (-not (Test-Path (Join-Path $InstallDir ".env"))) {
    Write-Warning "$InstallDir\.env not found. The service will start but WhatsApp sending will fail until it exists (see WHATSAPP_SETUP.md step 3)."
}

Write-Host "Installing service '$ServiceName' -> $node $entryPoint"

& $nssm install $ServiceName $node $entryPoint
& $nssm set $ServiceName AppDirectory $InstallDir
& $nssm set $ServiceName AppStdout "$InstallDir\logs\stdout.log"
& $nssm set $ServiceName AppStderr "$InstallDir\logs\stderr.log"
& $nssm set $ServiceName AppRotateFiles 1
& $nssm set $ServiceName AppRotateBytes 10485760
& $nssm set $ServiceName Start SERVICE_AUTO_START
& $nssm set $ServiceName DisplayName "OpenWA (WhatsApp Gateway)"
& $nssm set $ServiceName Description "Self-hosted WhatsApp API gateway used by the Office-Craft backend for booking notifications"

New-Item -ItemType Directory -Force -Path "$InstallDir\logs" | Out-Null

Write-Host "Starting service..."
& $nssm start $ServiceName

Write-Host "Done. Check status with: nssm status $ServiceName"
Write-Host "If the session shows disconnected after this, you may need to re-link via the dashboard at http://localhost:2785 - session data persists in data\sessions, but a service restart alone shouldn't require re-scanning the QR code."