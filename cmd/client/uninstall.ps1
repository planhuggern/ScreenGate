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
    $updateLock = $null
    $installDir = Join-Path $env:ProgramFiles 'ScreenGate'
    if (Test-Path -LiteralPath $installDir) {
        try { $updateLock = [IO.File]::Open((Join-Path $installDir 'update.lock'), 'OpenOrCreate', 'ReadWrite', 'None') }
        catch { throw 'En ScreenGate-oppdatering pagar. Vent litt og prov igjen.' }
    }
    try {
        $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
        if ($task) {
            Stop-ScheduledTask -InputObject $task
            Unregister-ScheduledTask -InputObject $task -Confirm:$false
        }
        if (Test-Path -LiteralPath $configuration) { Remove-Item -LiteralPath $configuration -Force }
        $remainingClients = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like 'ScreenGate Client*' })
        if ($remainingClients.Count -eq 0) {
            $updater = Get-ScheduledTask -TaskName 'ScreenGate Update' -ErrorAction SilentlyContinue
            if ($updater) { Unregister-ScheduledTask -InputObject $updater -Confirm:$false }
        }
        Write-Host "ScreenGate er avinstallert for $User. Andre brukeres oppgaver er beholdt."
        Write-Host 'Trekk ogsa tilbake enheten i foreldreoversikten. Lokal logg og tilstandsfil er beholdt.'
    } finally { if ($null -ne $updateLock) { $updateLock.Dispose() } }
}
