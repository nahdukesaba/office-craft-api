param(
    [string]$TaskName = "OfficeCraft-Tailscale-Funnel",
    [int]$Port = 8081,
    [string]$TailscaleExe = "C:\Program Files\Tailscale\tailscale.exe"
)

$ErrorActionPreference = "Stop"

if (!(Test-Path $TailscaleExe)) {
    throw "tailscale.exe not found at '$TailscaleExe'"
}

$tempScript = Join-Path $env:ProgramData "OfficeCraft\configure-funnel.ps1"

New-Item -ItemType Directory -Force -Path (Split-Path $tempScript) | Out-Null

@"
`$tailscale = "$TailscaleExe"

Start-Sleep -Seconds 30

while (`$true) {
    try {
        `$status = & `$tailscale status 2>`$null

        if (`$LASTEXITCODE -eq 0 -and `$status -match "active") {
            break
        }
    }
    catch {}

    Start-Sleep -Seconds 5
}

& `$tailscale funnel --bg $Port
"@ | Set-Content -Encoding UTF8 $tempScript

$action = New-ScheduledTaskAction `
    -Execute "powershell.exe" `
    -Argument "-ExecutionPolicy Bypass -File `"$tempScript`""

$trigger = New-ScheduledTaskTrigger -AtStartup

$trigger.Delay = "PT30S"

$principal = New-ScheduledTaskPrincipal `
    -UserId "SYSTEM" `
    -RunLevel Highest

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Principal $principal `
    -Settings $settings `
    -Force | Out-Null

Write-Host "Running task once..."

Start-ScheduledTask -TaskName $TaskName

Write-Host ""
Write-Host "Installed Scheduled Task:"
Write-Host "  $TaskName"