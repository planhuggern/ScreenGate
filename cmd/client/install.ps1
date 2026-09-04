param(
    [string]$ServerUrl = "http://10.0.0.20:8081/heartbeat",
    [string]$AdminPath = "/admin-4539c2c04a617d305f0d02215fc3b746",
    [string]$User
)

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Kjør install.ps1 fra et PowerShell-vindu åpnet som administrator."
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

    Write-Host "Velg brukeren som skal kjøre ScreenGate-klienten:"
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
Invoke-WebRequest -Uri $clientUrl -OutFile $clientPath

$action = New-ScheduledTaskAction -Execute $clientPath -Argument ("-server `"$ServerUrl`"")
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
Stop-ScheduledTask -TaskName "ScreenGate Client" -ErrorAction SilentlyContinue
Register-ScheduledTask -TaskName "ScreenGate Client" -Action $action -Trigger $trigger -Principal $taskPrincipal -Force | Out-Null
Start-ScheduledTask -TaskName "ScreenGate Client"

Write-Host "ScreenGate-klienten er installert for $User."
