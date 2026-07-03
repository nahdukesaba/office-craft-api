param(
    [string]$TaskName = "OfficeCraft-Tailscale-Funnel"
)

if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}

$script = Join-Path $env:ProgramData "OfficeCraft\configure-funnel.ps1"

if (Test-Path $script) {
    Remove-Item $script -Force
}

Write-Host "Removed Funnel task."