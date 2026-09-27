[CmdletBinding()]
param(
    [string]$ServerUrl = ''<# SCREENGATE_SERVER_DEFAULT #>,
    [string]$EnrollmentCode = ''<# SCREENGATE_ENROLLMENT_DEFAULT #>,
    [string]$User,
    [ValidatePattern('^$|^[a-fA-F0-9]{64}$')]
    [string]$ExpectedSha256,
    [switch]$SkipStart,
    [switch]$UpdatesOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Kjor install.ps1 fra et PowerShell-vindu apnet som administrator.'
}

if (-not $ServerUrl) {
    $ServerUrl = Read-Host 'ScreenGate-adresse, for eksempel http://192.168.1.10:8081/heartbeat'
}
$ServerUrl = $ServerUrl.Trim()
Write-Host "ScreenGate-server: $ServerUrl"
$serverUri = [Uri]$ServerUrl
if (-not $serverUri.IsAbsoluteUri -or $serverUri.Scheme -notin @('http', 'https') -or
    $serverUri.AbsolutePath -ne '/heartbeat' -or $serverUri.UserInfo -or $serverUri.Query -or $serverUri.Fragment) {
    throw 'ServerUrl ma vaere http(s)://SERVER:PORT/heartbeat uten passord, sporring eller fragment.'
}
if ($serverUri.Scheme -eq 'http' -and -not $serverUri.IsLoopback) {
    Write-Warning 'HTTP sender paringskode og enhetsnokkel ukryptert. Bruk HTTPS eller et nettverk du stoler pa.'
}
$serverOrigin = $serverUri.GetLeftPart([UriPartial]::Authority)
$clientUrl = "$serverOrigin/downloads/screengate-client.exe"

function Enable-ScreenGateUpdates {
    param([string]$Origin, [string]$InstallDirectory)
    $updateConfigPath = Join-Path $InstallDirectory 'update.json'
    $key = Invoke-RestMethod -Uri "$Origin/downloads/update-key.json" -TimeoutSec 30 -MaximumRedirection 0
    $parameters = New-Object Security.Cryptography.RSAParameters
    $parameters.Modulus = [Convert]::FromBase64String($key.modulus)
    $parameters.Exponent = [Convert]::FromBase64String($key.exponent)
    $rsa = [Security.Cryptography.RSA]::Create()
    try {
        $rsa.ImportParameters($parameters)
        if ($rsa.KeySize -lt 2048) { throw 'Serverens oppdateringsnokkel er for kort.' }
    } finally { $rsa.Dispose() }
    if (Test-Path -LiteralPath $updateConfigPath) {
        $existing = Get-Content -LiteralPath $updateConfigPath -Raw | ConvertFrom-Json
        if ($existing.origin -ne $Origin -or $existing.key.modulus -ne $key.modulus -or $existing.key.exponent -ne $key.exponent) {
            throw 'Oppdateringsserver eller nokkel er endret. Gjenopprett serverdatabasen eller fjern update.json som administrator for a godkjenne ny server.'
        }
    }
    $embeddedUpdater = 'SCREENGATE_UPDATER_BASE64'
    if ($embeddedUpdater -eq ('SCREENGATE_' + 'UPDATER_BASE64')) {
        $updaterBytes = [IO.File]::ReadAllBytes((Join-Path $PSScriptRoot 'update.ps1'))
    } else {
        $updaterBytes = [Convert]::FromBase64String($embeddedUpdater)
    }
    $updaterPath = Join-Path $InstallDirectory 'update.ps1'
    [IO.File]::WriteAllBytes($updaterPath, $updaterBytes)
    $updateConfiguration = @{ origin = $Origin; key = $key } | ConvertTo-Json -Depth 4
    [IO.File]::WriteAllText($updateConfigPath, $updateConfiguration, (New-Object Text.UTF8Encoding($false)))
    $powershellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $updateAction = New-ScheduledTaskAction -Execute $powershellPath -Argument ('-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $updaterPath + '"') -WorkingDirectory $InstallDirectory
    $updateTrigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(2) -RepetitionInterval (New-TimeSpan -Hours 1)
    $updatePrincipal = New-ScheduledTaskPrincipal -UserId 'S-1-5-18' -LogonType ServiceAccount -RunLevel Highest
    $updateSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Minutes 10) -MultipleInstances IgnoreNew
    Register-ScheduledTask -TaskName 'ScreenGate Update' -Action $updateAction -Trigger $updateTrigger -Principal $updatePrincipal -Settings $updateSettings -Description 'Kontrollerer signerte ScreenGate-oppdateringer hver time.' -Force | Out-Null
    Write-Host 'Automatisk klientoppdatering er aktivert. Kontrollerer hver time; paring og lasemodus beholdes.'
}

# Protect the updater, pinned key, and executable from modification by ordinary users.
$installDir = Join-Path $env:ProgramFiles 'ScreenGate'
New-Item -ItemType Directory -Path $installDir -Force | Out-Null
if ((Get-Item -LiteralPath $installDir).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Installasjonsmappen kan ikke vaere en lenke.' }
$installAcl = New-Object Security.AccessControl.DirectorySecurity
$installAcl.SetAccessRuleProtection($true, $false)
$installAcl.SetOwner((New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')))
foreach ($entry in @(@('S-1-5-18', 'FullControl'), @('S-1-5-32-544', 'FullControl'), @('S-1-5-32-545', 'ReadAndExecute'))) {
    $identity = New-Object Security.Principal.SecurityIdentifier($entry[0])
    $installAcl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($identity, $entry[1], 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
}
Set-Acl -LiteralPath $installDir -AclObject $installAcl
# Holding the file open prevents the hourly updater from racing this installation.
try { $installationLock = [IO.File]::Open((Join-Path $installDir 'update.lock'), 'OpenOrCreate', 'ReadWrite', 'None') }
catch { throw 'En ScreenGate-oppdatering pagar. Vent litt og prov igjen.' }
try {
if ($UpdatesOnly) {
    if (-not (Test-Path -LiteralPath (Join-Path $installDir 'screengate-client.exe'))) { throw 'Installer ScreenGate forst; -UpdatesOnly krever en eksisterende installasjon.' }
    Enable-ScreenGateUpdates -Origin $serverOrigin -InstallDirectory $installDir
    return
}

if (-not $User) {
    $users = @(Get-CimInstance Win32_UserProfile |
        Where-Object { -not $_.Special -and $_.LocalPath } |
        ForEach-Object {
            try {
                [pscustomobject]@{ User = (New-Object Security.Principal.SecurityIdentifier($_.SID)).Translate([Security.Principal.NTAccount]).Value }
            } catch { Write-Verbose "Hopper over profil som ikke kan identifiseres: $($_.Exception.Message)" }
        } | Sort-Object User -Unique)
    if ($users.Count -eq 0) { throw 'Fant ingen lokale brukerprofiler. Oppgi -User DATAMASKIN\bruker.' }
    Write-Host 'Velg Windows-brukeren som skal styres av ScreenGate:'
    for ($i = 0; $i -lt $users.Count; $i++) { Write-Host "[$($i + 1)] $($users[$i].User)" }
    $selection = 0
    if (-not [int]::TryParse((Read-Host 'Nummer'), [ref]$selection) -or $selection -lt 1 -or $selection -gt $users.Count) {
        throw 'Ugyldig brukervalg.'
    }
    $User = $users[$selection - 1].User
}
$account = New-Object Security.Principal.NTAccount($User.Replace('/', '\'))
$userSid = $account.Translate([Security.Principal.SecurityIdentifier])
$User = $userSid.Translate([Security.Principal.NTAccount]).Value
$installDir = Join-Path $env:ProgramFiles 'ScreenGate'
$clientPath = Join-Path $installDir 'screengate-client.exe'
$configRoot = Join-Path $env:ProgramData 'ScreenGate'
$configDir = Join-Path $configRoot $userSid.Value
$configPath = Join-Path $configDir 'client.json'
$taskName = "ScreenGate Client $($userSid.Value)"

# Stop old clients before inspecting configuration or asking for a new
# pairing code. An existing logon task must not enforce the old state while
# the administrator is still preparing this installation.
$stoppedTasks = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object {
    $_.TaskName -like 'ScreenGate Client*' -and @($_.Actions | Where-Object { $_.Execute -eq $clientPath }).Count -gt 0
})
foreach ($existingTask in $stoppedTasks) {
    Stop-ScheduledTask -InputObject $existingTask -ErrorAction SilentlyContinue
    Disable-ScheduledTask -InputObject $existingTask -ErrorAction SilentlyContinue | Out-Null
}
# Deleting a Scheduled Task or exe does not terminate a process it already
# started. The process may still be resident even when ExecutablePath is no
# longer queryable, so use the unique ScreenGate process name here.
Get-Process -Name 'screengate-client' -ErrorAction SilentlyContinue |
    Stop-Process -Force -ErrorAction SilentlyContinue

# Preserve pairing on upgrades; a new code deliberately replaces this user's pairing.
$configuration = $null
if (-not $EnrollmentCode -and (Test-Path -LiteralPath $configPath)) {
    $configuration = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $configuration.server = ([string]$configuration.server).Trim()
    if ($configuration.server -ne $ServerUrl -or -not $configuration.token -or -not $configuration.device_id -or -not $configuration.user) {
        throw 'Eksisterende paring passer ikke til denne serveren. Oppgi en ny -EnrollmentCode.'
    }
}
if (-not $configuration -and -not $EnrollmentCode) {
    $EnrollmentCode = Read-Host 'Paringskode fra ScreenGate-administrasjonen (gyldig i 15 minutter)'
}
if (-not $configuration -and -not $EnrollmentCode) { throw 'En paringskode er pakrevd.' }

Write-Host 'Velg modus for ScreenGate:'
Write-Host '[1] Testmodus - registrer tidsbruk uten a lase Windows (standard)'
Write-Host '[2] Aktiver lasing - Windows kan lases nar kvoten eller reglene krever det'
do {
    $modeSelection = (Read-Host 'Valg [1]').Trim()
    if ($modeSelection -notin @('', '1', '2')) {
        Write-Host 'Ugyldig valg. Skriv 1 eller 2, eller trykk Enter for testmodus.'
    }
} while ($modeSelection -notin @('', '1', '2'))
$EnableLocking = $modeSelection -eq '2'

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
$temporaryClientPath = Join-Path $installDir ('.client-' + [Guid]::NewGuid().ToString('N') + '.exe')
$backupClientPath = Join-Path $installDir ('.backup-' + [Guid]::NewGuid().ToString('N') + '.exe')
$binaryReplaced = $false
$completed = $false
$preserveBackup = $false
try {
    # Download and validate before replacing an existing installation.
    Invoke-WebRequest -UseBasicParsing -Uri $clientUrl -OutFile $temporaryClientPath -TimeoutSec 60 -MaximumRedirection 0
    if (-not $ExpectedSha256) {
        $checksum = (Invoke-WebRequest -UseBasicParsing -Uri "$clientUrl.sha256" -TimeoutSec 20 -MaximumRedirection 0).Content
        if ($checksum -notmatch '^([a-fA-F0-9]{64})\s') { throw 'Serveren returnerte en ugyldig SHA-256.' }
        $ExpectedSha256 = $Matches[1]
    }
    $stream = [IO.File]::OpenRead($temporaryClientPath)
    try {
        if ($stream.Length -lt 1024 -or $stream.ReadByte() -ne 0x4D -or $stream.ReadByte() -ne 0x5A) {
            throw 'Nedlastingen er ikke en gyldig Windows-klient.'
        }
    } finally { $stream.Dispose() }
    if ((Get-FileHash -LiteralPath $temporaryClientPath -Algorithm SHA256).Hash -ne $ExpectedSha256) {
        throw 'SHA-256 stemmer ikke. Installasjonen er avbrutt.'
    }

    Enable-ScreenGateUpdates -Origin $serverOrigin -InstallDirectory $installDir

    if (-not $configuration) {
        $body = @{ code = $EnrollmentCode.Trim(); device_id = [Environment]::MachineName; user = $User } | ConvertTo-Json -Compress
        $pairing = Invoke-RestMethod -Method Post -Uri "$serverOrigin/enroll" -ContentType 'application/json' -Body $body -TimeoutSec 20 -MaximumRedirection 0
        if (-not $pairing.token -or -not $pairing.device_id -or -not $pairing.user) { throw 'Serveren returnerte en ufullstendig paring.' }
        $configuration = @{ server = $ServerUrl; token = $pairing.token; device_id = $pairing.device_id; user = $pairing.user }
    }

    # Only administrators and SYSTEM may replace configuration. The controlled
    # user needs read access to authenticate; tokens are never put on task arguments.
    New-Item -ItemType Directory -Path $configRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
        $identity = New-Object Security.Principal.SecurityIdentifier($sid)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    $readRule = New-Object Security.AccessControl.FileSystemAccessRule($userSid, 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($readRule)
    Set-Acl -LiteralPath $configDir -AclObject $acl
    # Reinstalling without explicit opt-in always returns to safe test mode.
    $configuration = [pscustomobject]$configuration
    $configuration | Add-Member -NotePropertyName enable_locking -NotePropertyValue ([bool]$EnableLocking) -Force
    $configJson = $configuration | ConvertTo-Json -Compress
    # Explicit UTF-8 without BOM also works in Windows PowerShell 5.1.
    [IO.File]::WriteAllText($configPath, $configJson, (New-Object Text.UTF8Encoding($false)))

    for ($attempt = 1; $attempt -le 10; $attempt++) {
        try {
            if (Test-Path -LiteralPath $clientPath) {
                [IO.File]::Replace($temporaryClientPath, $clientPath, $backupClientPath)
            } else {
                [IO.File]::Move($temporaryClientPath, $clientPath)
            }
            $binaryReplaced = $true
            break
        } catch {
            if ($attempt -eq 10) { throw 'Kunne ikke erstatte klienten. Lukk manuelt startede ScreenGate-klienter og prov igjen.' }
            Start-Sleep -Seconds 1
        }
    }

    # Explicit flags also make an older client exit on an unknown flag instead
    # of silently ignoring the new configuration and enabling locking.
    $modeArgument = '-test-mode'
    if ($EnableLocking) { $modeArgument = '-enable-locking' }
    $action = New-ScheduledTaskAction -Execute $clientPath -Argument ($modeArgument + ' -config "' + $configPath + '"') -WorkingDirectory $installDir
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
    $taskPrincipal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $taskPrincipal -Settings $settings -Description 'ScreenGate skjermtid for denne Windows-brukeren.' -Force | Out-Null
    foreach ($existingTask in $stoppedTasks) {
        if ($existingTask.TaskName -eq 'ScreenGate Client') {
            Unregister-ScheduledTask -InputObject $existingTask -Confirm:$false
        } elseif ($existingTask.TaskName -ne $taskName) {
            Enable-ScheduledTask -InputObject $existingTask | Out-Null
            Start-ScheduledTask -InputObject $existingTask
        }
    }
    if (-not $SkipStart) { Start-ScheduledTask -TaskName $taskName }
    $completed = $true
    Write-Host "ScreenGate er installert for $User (ScreenGate-bruker: $($configuration.user))."
    if ($EnableLocking) {
        Write-Host 'Lasing er AKTIVERT.'
    } else {
        Write-Host 'TESTMODUS: ScreenGate registrerer tidsbruk, men laser ikke Windows, heller ikke etter omstart.'
    }
    Write-Host 'Logg: %LOCALAPPDATA%\ScreenGate\client.log i den valgte brukerens profil.'
    if ($SkipStart) { Write-Host 'Klienten starter ved neste innlogging.' }
} finally {
    if (-not $completed -and $binaryReplaced -and (Test-Path -LiteralPath $backupClientPath)) {
        try { [IO.File]::Replace($backupClientPath, $clientPath, $temporaryClientPath) } catch {
            $preserveBackup = $true
            Write-Warning "Gjenoppretting feilet. Gammel klient er bevart i $backupClientPath. $($_.Exception.Message)"
        }
    }
    if (-not $completed -and $stoppedTasks.Count -gt 0) {
        Write-Warning 'Eksisterende ScreenGate-oppgaver er holdt stoppet etter en mislykket installasjon for å unnga umiddelbar utelasing.'
    }
    foreach ($temporary in @($temporaryClientPath, $backupClientPath)) {
        if ($temporary -eq $backupClientPath -and $preserveBackup) { continue }
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}

} finally { $installationLock.Dispose() }
