[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot '..\cmd\client\update.ps1')

# Exercise real signature verification and atomic file replacement. Only the
# network, task scheduler and sleeps are replaced; no installed client is touched.
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('screengate-update-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$signer = New-Object Security.Cryptography.RSACryptoServiceProvider(2048)
$public = $signer.ExportParameters($false)
$key = @{ modulus = [Convert]::ToBase64String($public.Modulus); exponent = [Convert]::ToBase64String($public.Exponent) }
$configuration = @{ origin = 'http://screen.test'; key = $key }
$configuration | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $testRoot 'update.json')
$script:testBinary = New-Object byte[] 2048
$script:testBinary[0] = 0x4D
$script:testBinary[1] = 0x5A
$script:testBinary[2] = 42
$script:testScenario = ''
$script:testStops = 0
$script:testStarts = 0
$script:testDownloads = 0
$script:installedPath = Join-Path $testRoot 'screengate-client.exe'
$script:oldBinary = [Text.Encoding]::ASCII.GetBytes('MZ old client')

function Assert-UpdateTest($Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Get-TestHash([byte[]]$Bytes) {
    $hash = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($hash.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant() }
    finally { $hash.Dispose() }
}
function Invoke-WebRequest {
    param($Uri, $OutFile, $TimeoutSec, $MaximumRedirection, [switch]$UseBasicParsing)
    if ($script:testScenario -eq 'offline') { throw 'Simulated connection failure' }
    if ($OutFile) {
        $script:testDownloads++
        if ($script:testScenario -eq 'bad-download') { [IO.File]::WriteAllBytes($OutFile, $script:oldBinary) }
        else { [IO.File]::WriteAllBytes($OutFile, $script:testBinary) }
        return
    }
    $nonce = ([Uri]$Uri).Query.Substring('?nonce='.Length)
    if ($script:testScenario -eq 'replayed') { $nonce = 'old-nonce' }
    $payload = [Text.Encoding]::UTF8.GetBytes((@{ protocol = 1; nonce = $nonce; sha256 = (Get-TestHash $script:testBinary); size = $script:testBinary.Length } | ConvertTo-Json -Compress))
    $signature = $signer.SignData($payload, [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.RSASignaturePadding]::Pkcs1)
    if ($script:testScenario -eq 'tampered') { $signature[0] = $signature[0] -bxor 1 }
    return @{ Content = (@{ payload = [Convert]::ToBase64String($payload); signature = [Convert]::ToBase64String($signature) } | ConvertTo-Json -Compress) }
}
function Get-ScheduledTask {
    param($TaskName, $TaskPath)
    $state = 'Running'
    if ($TaskName -and $script:testScenario -eq 'startup-failure') { $state = 'Ready' }
    return [pscustomobject]@{ TaskName = 'ScreenGate Client test'; TaskPath = '\'; State = $state; Actions = @([pscustomobject]@{ Execute = $script:installedPath; Arguments = '-test-mode -config preserved.json' }) }
}
function Stop-ScheduledTask { param($InputObject, $ErrorAction) $script:testStops++ }
function Start-ScheduledTask { param($InputObject, $ErrorAction) $script:testStarts++ }
function Start-Sleep { param($Seconds) }

try {
    foreach ($scenario in @('offline', 'tampered', 'replayed', 'bad-download')) {
        $script:testScenario = $scenario
        [IO.File]::WriteAllBytes($script:installedPath, $script:oldBinary)
        $failed = $false
        try { Invoke-ScreenGateUpdate -InstallDirectory $testRoot } catch { $failed = $true }
        Assert-UpdateTest $failed "$scenario should have failed"
        Assert-UpdateTest ((Get-ScreenGateFileHash -LiteralPath $script:installedPath) -eq (Get-TestHash $script:oldBinary)) "$scenario changed installed client"
        Assert-UpdateTest ($script:testStops -eq 0) "$scenario stopped the running client"
    }
    $script:testScenario = 'success'
    Invoke-ScreenGateUpdate -InstallDirectory $testRoot | Out-Null
    Assert-UpdateTest ((Get-ScreenGateFileHash -LiteralPath $script:installedPath) -eq (Get-TestHash $script:testBinary)) 'New binary was not installed'
    Assert-UpdateTest ($script:testStops -eq 1 -and $script:testStarts -eq 1) 'Running client was not restarted exactly once'
    $downloads = $script:testDownloads
    Invoke-ScreenGateUpdate -InstallDirectory $testRoot | Out-Null
    Assert-UpdateTest ($script:testDownloads -eq $downloads -and $script:testStops -eq 1) 'Unchanged client was downloaded or restarted'
    $script:testScenario = 'startup-failure'
    [IO.File]::WriteAllBytes($script:installedPath, $script:oldBinary)
    $failed = $false
    try { Invoke-ScreenGateUpdate -InstallDirectory $testRoot } catch { $failed = $true }
    Assert-UpdateTest $failed 'Startup failure was not detected'
    Assert-UpdateTest ((Get-ScreenGateFileHash -LiteralPath $script:installedPath) -eq (Get-TestHash $script:oldBinary)) 'Previous client was not restored'
    Assert-UpdateTest ($script:testStarts -eq 3) 'Previous client was not restarted after rollback'
    Write-Host 'Updater tests passed: signature, replay, offline, download integrity, replacement, no-op, rollback.'
} finally {
    $signer.Dispose()
    # Only remove the freshly-created test directory after checking its boundary.
    $resolvedTestRoot = [IO.Path]::GetFullPath($testRoot)
    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedTestRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or
        (Split-Path -Leaf $resolvedTestRoot) -notlike 'screengate-update-test-*') { throw 'Unexpected test cleanup path.' }
    Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force
}

