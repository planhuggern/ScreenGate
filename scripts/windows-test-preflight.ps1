[CmdletBinding()]
param(
    [string]$ClientBinary = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $ClientBinary) {
    $ClientBinary = Join-Path $PSScriptRoot '..\.artifacts\step6\screengate-client.exe'
}
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'Run this read-only check in the Windows account intended for testing.'
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
# An unelevated administrator is still an administrator account. Inspect group
# membership as well as elevation so it is not mistaken for a standard account.
$groupIds = @($identity.Groups | ForEach-Object { $_.Value })
$binary = $null
if (Test-Path -LiteralPath $ClientBinary -PathType Leaf) {
    $file = Get-Item -LiteralPath $ClientBinary
    $stream = [IO.File]::OpenRead($file.FullName)
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $hash = ([BitConverter]::ToString($hasher.ComputeHash($stream))).Replace('-', '')
    } finally {
        $stream.Dispose()
        $hasher.Dispose()
    }
    $binary = [pscustomobject]@{
        Path = $file.FullName
        Bytes = $file.Length
        SHA256 = $hash
    }
}

# Never read command lines, tokens, configuration or another user's log.
$clients = @(Get-Process -Name 'screengate-client' -ErrorAction SilentlyContinue |
    Select-Object Id, SessionId)

[pscustomobject]@{
    Status = 'Preflight only - interactive behavior has not been verified'
    Account = $identity.Name
    SessionId = [Diagnostics.Process]::GetCurrentProcess().SessionId
    UserInteractive = [Environment]::UserInteractive
    AdministratorsGroup = $groupIds -contains 'S-1-5-32-544'
    Elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    CodexSandboxAccount = $identity.Name -match '\\CodexSandbox[^\\]*$'
    ClientBinary = $binary
    ExistingClientProcesses = $clients
    ChangesMade = $false
} | ConvertTo-Json -Depth 4
