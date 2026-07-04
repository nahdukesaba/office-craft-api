$ErrorActionPreference = "Stop"

# -------------------------------------------------------
# Configuration
# -------------------------------------------------------

$ScriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Resolve-Path (Join-Path $ScriptDir "..")

$EnvFile = Join-Path $ProjectDir ".env"
$CsvFile = Join-Path $ProjectDir "users.csv"

# -------------------------------------------------------
# Load .env
# -------------------------------------------------------

if (!(Test-Path $EnvFile)) {
    throw ".env not found at $EnvFile"
}

Get-Content $EnvFile | ForEach-Object {

    $line = $_.Trim()

    if ($line -eq "") { return }
    if ($line.StartsWith("#")) { return }

    $parts = $line -split "=",2

    if ($parts.Count -eq 2) {
        [Environment]::SetEnvironmentVariable(
            $parts[0].Trim(),
            $parts[1].Trim(),
            "Process"
        )
    }
}

$SupabaseUrl = $env:SUPABASE_URL
$ServiceRoleKey = $env:SUPABASE_SERVICE_ROLE_KEY

if ([string]::IsNullOrWhiteSpace($SupabaseUrl)) {
    throw "SUPABASE_URL missing in .env"
}

if ([string]::IsNullOrWhiteSpace($ServiceRoleKey)) {
    throw "SUPABASE_SERVICE_ROLE_KEY missing in .env"
}

# -------------------------------------------------------
# CSV
# -------------------------------------------------------

if (!(Test-Path $CsvFile)) {
    throw "users.csv not found"
}

$Users = Import-Csv $CsvFile

# -------------------------------------------------------
# Headers
# -------------------------------------------------------

$Headers = @{
    apikey        = $ServiceRoleKey
    Authorization = "Bearer $ServiceRoleKey"
    "Content-Type" = "application/json"
}

$ProfileHeaders = @{
    apikey        = $ServiceRoleKey
    Authorization = "Bearer $ServiceRoleKey"
    "Content-Type" = "application/json"
    Prefer = "resolution=merge-duplicates,return=representation"
}

# -------------------------------------------------------
# Stats
# -------------------------------------------------------

$Created = 0
$Skipped = 0
$Errors = @()

# -------------------------------------------------------
# Register users
# -------------------------------------------------------

foreach ($User in $Users) {

    $Email = "$($User.email)".Trim()
    $Password = "$($User.password)".Trim()
    $FullName = "$($User.full_name)".Trim()

    # Skip completely empty rows
    if (
        [string]::IsNullOrWhiteSpace($Email) -and
        [string]::IsNullOrWhiteSpace($Password) -and
        [string]::IsNullOrWhiteSpace($FullName)
    ) {
        $Skipped++
        continue
    }

    try {

        Write-Host "Creating $Email ..."

        $SignupBody = @{
            email    = $Email
            password = $Password
        } | ConvertTo-Json

        $Signup = Invoke-RestMethod `
            -Method POST `
            -Uri "$SupabaseUrl/auth/v1/signup" `
            -Headers $Headers `
            -Body $SignupBody

        if (-not $Signup.user.id) {
            throw "Signup succeeded but no user id returned."
        }

        $UserId = $Signup.user.id

        $Profile = @{
            id = $UserId
            email = $Email
            full_name = $FullName
            role = "user"
            status = "pending"
        }

        Invoke-RestMethod `
            -Method POST `
            -Uri "$SupabaseUrl/rest/v1/app_users?on_conflict=id" `
            -Headers $ProfileHeaders `
            -Body (@($Profile) | ConvertTo-Json)

        Write-Host "  OK"

        $Created++

    }
    catch {

        $Skipped++

        $Message = $_.Exception.Message

        try {
            $Reader = New-Object System.IO.StreamReader(
                $_.Exception.Response.GetResponseStream()
            )
            $Message = $Reader.ReadToEnd()
        }
        catch {}

        $Errors += "[$Email] $Message"

        Write-Host "  FAILED"
    }
}

# -------------------------------------------------------
# Summary
# -------------------------------------------------------

Write-Host ""
Write-Host "======================================="
Write-Host "Completed"
Write-Host "======================================="
Write-Host "Created : $Created"
Write-Host "Skipped : $Skipped"
Write-Host ""

if ($Errors.Count -gt 0) {

    Write-Host "Errors:"
    Write-Host ""

    foreach ($ErrorMessage in $Errors) {
        Write-Host $ErrorMessage
    }

}
else {
    Write-Host "No errors."
}