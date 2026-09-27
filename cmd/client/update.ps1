[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-ScreenGateFileHash {
    param([string]$LiteralPath)
    $stream = [IO.File]::OpenRead($LiteralPath)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha256.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $sha256.Dispose() }
}

function Read-ScreenGateManifest {
    param($Configuration)
    $nonceBytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($nonceBytes) } finally { $rng.Dispose() }
    $nonce = ([BitConverter]::ToString($nonceBytes)).Replace('-', '').ToLowerInvariant()
    $response = Invoke-WebRequest -UseBasicParsing -Uri ($Configuration.origin + '/downloads/update.json?nonce=' + $nonce) -TimeoutSec 30 -MaximumRedirection 0
    if ($response.Content.Length -gt 16384) { throw 'Update manifest is too large.' }
    $envelope = $response.Content | ConvertFrom-Json
    $payload = [Convert]::FromBase64String($envelope.payload)
    $signature = [Convert]::FromBase64String($envelope.signature)
    $parameters = New-Object Security.Cryptography.RSAParameters
    $parameters.Modulus = [Convert]::FromBase64String($Configuration.key.modulus)
    $parameters.Exponent = [Convert]::FromBase64String($Configuration.key.exponent)
    $rsa = [Security.Cryptography.RSA]::Create()
    try {
        $rsa.ImportParameters($parameters)
        if (-not $rsa.VerifyData($payload, $signature, [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.RSASignaturePadding]::Pkcs1)) {
            throw 'Update signature does not match the pinned server key.'
        }
    } finally { $rsa.Dispose() }
    $manifest = [Text.Encoding]::UTF8.GetString($payload) | ConvertFrom-Json
    if ($manifest.protocol -ne 1 -or $manifest.nonce -cne $nonce -or $manifest.sha256 -notmatch '^[a-fA-F0-9]{64}$' -or
        $manifest.size -lt 1024 -or $manifest.size -gt 134217728) {
        throw 'Invalid or replayed update manifest.'
    }
    return $manifest
}

function Invoke-ScreenGateUpdate {
    param([Parameter(Mandatory = $true)][string]$InstallDirectory)
    $configuration = Get-Content -LiteralPath (Join-Path $InstallDirectory 'update.json') -Raw | ConvertFrom-Json
    $origin = [Uri]$configuration.origin
    if (-not $origin.IsAbsoluteUri -or $origin.Scheme -notin @('http', 'https') -or $origin.UserInfo -or
        $origin.Query -or $origin.Fragment -or $origin.AbsolutePath -ne '/') { throw 'Invalid update origin.' }
    $clientPath = Join-Path $InstallDirectory 'screengate-client.exe'
    $manifest = Read-ScreenGateManifest $configuration
    if ((Test-Path -LiteralPath $clientPath) -and (Get-ScreenGateFileHash -LiteralPath $clientPath) -eq $manifest.sha256) {
        return
    }
    $temporaryPath = Join-Path $InstallDirectory ('.update-' + [Guid]::NewGuid().ToString('N') + '.exe')
    $backupPath = Join-Path $InstallDirectory 'screengate-client.previous.exe'
    $stoppedTasks = @()
    $replaced = $false
    try {
        # Nothing is stopped until the complete download has been authenticated.
        Invoke-WebRequest -UseBasicParsing -Uri ($configuration.origin + '/downloads/screengate-client.exe') -OutFile $temporaryPath -TimeoutSec 120 -MaximumRedirection 0
        if ((Get-Item -LiteralPath $temporaryPath).Length -ne $manifest.size -or
            (Get-ScreenGateFileHash -LiteralPath $temporaryPath) -ne $manifest.sha256) {
            throw 'Client download does not match the signed manifest.'
        }
        $stream = [IO.File]::OpenRead($temporaryPath)
        try {
            if ($stream.ReadByte() -ne 0x4D -or $stream.ReadByte() -ne 0x5A) { throw 'Client download is not a Windows executable.' }
        } finally { $stream.Dispose() }

        $tasks = @(Get-ScheduledTask | Where-Object {
            $_.TaskName -like 'ScreenGate Client*' -and @($_.Actions | Where-Object { $_.Execute -eq $clientPath }).Count -gt 0
        })
        foreach ($task in $tasks) {
            if ($task.State -eq 'Running') {
                $stoppedTasks += $task
                Stop-ScheduledTask -InputObject $task
            }
        }
        # Replace is atomic and leaves the previous executable available for rollback.
        for ($attempt = 1; $attempt -le 10; $attempt++) {
            try {
                if (Test-Path -LiteralPath $clientPath) {
                    [IO.File]::Replace($temporaryPath, $clientPath, $backupPath)
                } else {
                    [IO.File]::Move($temporaryPath, $clientPath)
                }
                $replaced = $true
                break
            } catch {
                if ($attempt -eq 10) { throw }
                Start-Sleep -Seconds 1
            }
        }
        foreach ($task in $stoppedTasks) { Start-ScheduledTask -InputObject $task }
        if ($stoppedTasks.Count -gt 0) {
            Start-Sleep -Seconds 8
            foreach ($task in $stoppedTasks) {
                $current = Get-ScheduledTask -TaskName $task.TaskName -TaskPath $task.TaskPath
                if ($current.State -ne 'Running') { throw "Updated client did not stay running: $($task.TaskName)" }
            }
        }
        Write-Output ('Updated client to SHA-256 ' + $manifest.sha256)
    } catch {
        $failure = $_
        if ($replaced -and (Test-Path -LiteralPath $backupPath)) {
            foreach ($task in $stoppedTasks) { Stop-ScheduledTask -InputObject $task -ErrorAction SilentlyContinue }
            for ($attempt = 1; $attempt -le 10; $attempt++) {
                try {
                    [IO.File]::Replace($backupPath, $clientPath, $temporaryPath)
                    break
                } catch {
                    if ($attempt -eq 10) { throw "Rollback failed; previous client remains at $backupPath. $_" }
                    Start-Sleep -Seconds 1
                }
            }
        }
        foreach ($task in $stoppedTasks) { Start-ScheduledTask -InputObject $task -ErrorAction SilentlyContinue }
        throw $failure
    } finally {
        if (Test-Path -LiteralPath $temporaryPath) { Remove-Item -LiteralPath $temporaryPath -Force }
    }
}

# Dot-sourcing exposes the functions for integration tests without starting an update.
if ($MyInvocation.InvocationName -ne '.') {
    $logPath = Join-Path $PSScriptRoot 'update.log'
    $lockStream = $null
    try {
        # Shared with install/uninstall to avoid replacing the executable concurrently.
        $lockStream = [IO.File]::Open((Join-Path $PSScriptRoot 'update.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
        if ((Test-Path -LiteralPath $logPath) -and (Get-Item -LiteralPath $logPath).Length -gt 1048576) {
            Move-Item -LiteralPath $logPath -Destination ($logPath + '.1') -Force
        }
        Invoke-ScreenGateUpdate -InstallDirectory $PSScriptRoot | ForEach-Object {
            Add-Content -LiteralPath $logPath -Value ((Get-Date -Format o) + ' ' + $_)
        }
    } catch {
        Add-Content -LiteralPath $logPath -Value ((Get-Date -Format o) + ' Update failed: ' + $_)
        exit 1
    } finally {
        if ($null -ne $lockStream) { $lockStream.Dispose() }
    }
}
