param(
    [string]$ServerUrl = "http://10.0.0.20:8081/heartbeat",
    [string]$AdminPath = "/admin-4539c2c04a617d305f0d02215fc3b746",
    [string]$User
)

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Kjor install.ps1 fra et PowerShell-vindu apnet som administrator."
}

$serverUri = [Uri]$ServerUrl
$clientUrl = "$($serverUri.Scheme)://$($serverUri.Authority)$AdminPath/downloads/screengate-client.exe"
$installDir = Join-Path $env:ProgramFiles "ScreenGate"
$clientPath = Join-Path $installDir "screengate-client.exe"
if (-not $User) {
    $users = Get-CimInstance Win32_UserProfile |
        Where-Object { -not $_.Special -and $_.LocalPath } |
        ForEach-Object {
            try {
                [pscustomobject]@{
                    User = (New-Object Security.Principal.SecurityIdentifier($_.SID)).Translate([Security.Principal.NTAccount]).Value
                }
            } catch {}
        } |
        Sort-Object User -Unique

    if (-not $users) {
        throw "Fant ingen lokale brukerprofiler. Oppgi -User DATAMASKIN\\bruker."
    }

    Write-Host "Velg brukeren som skal kjore ScreenGate-klienten:"
    for ($i = 0; $i -lt $users.Count; $i++) {
        Write-Host "[$($i + 1)] $($users[$i].User)"
    }
    $selection = [int](Read-Host "Nummer") - 1
    if ($selection -lt 0 -or $selection -ge $users.Count) {
        throw "Ugyldig brukervalg."
    }
    $User = $users[$selection].User
}
$User = $User.Replace('/', '\')

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Stop-ScheduledTask -TaskName "ScreenGate Client" -ErrorAction SilentlyContinue

$temporaryClientPath = "$clientPath.new"
Remove-Item -LiteralPath $temporaryClientPath -Force -ErrorAction SilentlyContinue
Invoke-WebRequest -Uri $clientUrl -OutFile $temporaryClientPath

for ($attempt = 1; $attempt -le 10; $attempt++) {
    try {
        Remove-Item -LiteralPath $clientPath -Force -ErrorAction Stop
        Move-Item -LiteralPath $temporaryClientPath -Destination $clientPath -ErrorAction Stop
        break
    } catch {
        if ($attempt -eq 10) {
            throw "Kunne ikke erstatte den gamle ScreenGate-klienten. Lukk eventuelle manuelt startede ScreenGate-klienter og prov igjen."
        }
        Start-Sleep -Seconds 1
    }
}

$action = New-ScheduledTaskAction -Execute $clientPath -Argument ("-server `"$ServerUrl`"")
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName "ScreenGate Client" -Action $action -Trigger $trigger -Principal $taskPrincipal -Force | Out-Null
Start-ScheduledTask -TaskName "ScreenGate Client"

Write-Host "ScreenGate-klienten er installert for $User."
