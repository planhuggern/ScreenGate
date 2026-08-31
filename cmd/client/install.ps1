param(
    [string]$ServerUrl = "http://10.0.0.20:8081/heartbeat"
)

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Kjør install.ps1 fra et PowerShell-vindu åpnet som administrator."
}

$serverUri = [Uri]$ServerUrl
$clientUrl = "$($serverUri.Scheme)://$($serverUri.Authority)/downloads/screengate-client.exe"
$installDir = Join-Path $env:ProgramFiles "ScreenGate"
$clientPath = Join-Path $installDir "screengate-client.exe"
$currentUser = [Security.Principal.WindowsIdentity]::GetCurrent().Name

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Invoke-WebRequest -Uri $clientUrl -OutFile $clientPath

$action = New-ScheduledTaskAction -Execute $clientPath -Argument ("-server `"$ServerUrl`"")
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName "ScreenGate Client" -Action $action -Trigger $trigger -Principal $taskPrincipal -Force | Out-Null
Start-ScheduledTask -TaskName "ScreenGate Client"

Write-Host "ScreenGate-klienten er installert for $currentUser."
