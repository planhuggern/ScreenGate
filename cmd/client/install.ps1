param(
    [string]$ServerUrl = "http://10.0.0.20:8081/heartbeat",
    [string]$User
)

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Kjør install.ps1 fra et PowerShell-vindu åpnet som administrator."
}

$serverUri = [Uri]$ServerUrl
$clientUrl = "$($serverUri.Scheme)://$($serverUri.Authority)/downloads/screengate-client.exe"
$installDir = Join-Path $env:ProgramFiles "ScreenGate"
$clientPath = Join-Path $installDir "screengate-client.exe"
if (-not $User) {
    $User = (Get-CimInstance Win32_ComputerSystem).UserName
}
if (-not $User) {
    throw "Fant ingen innlogget Windows-bruker. Oppgi -User DOMENE\\bruker."
}

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Invoke-WebRequest -Uri $clientUrl -OutFile $clientPath

$action = New-ScheduledTaskAction -Execute $clientPath -Argument ("-server `"$ServerUrl`"")
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName "ScreenGate Client" -Action $action -Trigger $trigger -Principal $taskPrincipal -Force | Out-Null
Start-ScheduledTask -TaskName "ScreenGate Client"

Write-Host "ScreenGate-klienten er installert for $User."
