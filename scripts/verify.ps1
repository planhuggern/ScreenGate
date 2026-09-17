[CmdletBinding()]
param([switch]$SkipDocker, [switch]$SkipRace)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go-testene feilet.' }
    if (-not $SkipRace) {
        & go test -race ./...
        if ($LASTEXITCODE -ne 0) { throw 'Samtidighetstestene feilet (race krever C-kompilator).' }
    }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet feilet.' }
    New-Item -ItemType Directory -Path '.artifacts' -Force | Out-Null
    & go build -o .artifacts/screengate.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Serverbygget feilet.' }
    & go build -ldflags '-H=windowsgui' -o .artifacts/screengate-client.exe ./cmd/client
    if ($LASTEXITCODE -ne 0) { throw 'Windows-klienten kunne ikke bygges.' }
    foreach ($script in @('cmd/client/install.ps1', 'cmd/client/uninstall.ps1')) {
        $tokens = $null
        $parseErrors = $null
        [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $projectRoot $script), [ref]$tokens, [ref]$parseErrors) | Out-Null
        if ($parseErrors.Count) { throw "Ugyldig PowerShell i ${script}: $parseErrors" }
    }
    if (-not $SkipDocker) {
        & docker build -t screengate:verification .
        if ($LASTEXITCODE -ne 0) { throw 'Docker-bygget feilet.' }
    }
    Write-Host 'ScreenGate er verifisert. Ingen Windows-klient er installert eller startet.'
} finally { Pop-Location }
