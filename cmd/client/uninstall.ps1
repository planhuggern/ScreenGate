[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory = $true)]
    [string]$User
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Kjor avinstalleringen som administrator.'
}
$account = New-Object Security.Principal.NTAccount($User.Replace('/', '\'))
$sid = $account.Translate([Security.Principal.SecurityIdentifier]).Value
$taskName = "ScreenGate Client $sid"
$configuration = Join-Path (Join-Path (Join-Path $env:ProgramData 'ScreenGate') $sid) 'client.json'

if ($PSCmdlet.ShouldProcess($User, 'Fjern ScreenGate-oppgaven og enhetsnokkelen for denne Windows-brukeren')) {
    $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($task) {
        Stop-ScheduledTask -InputObject $task
        Unregister-ScheduledTask -InputObject $task -Confirm:$false
    }
    if (Test-Path -LiteralPath $configuration) { Remove-Item -LiteralPath $configuration -Force }
    Write-Host "ScreenGate er avinstallert for $User. Andre brukeres oppgaver er beholdt."
    Write-Host 'Trekk ogsa tilbake enheten i foreldreoversikten. Lokal logg og tilstandsfil er beholdt.'
}
