[CmdletBinding()]
param([string]$Image = 'screengate:verification')

$ErrorActionPreference = 'Stop'
$verificationName = 'screengate-check-' + [Guid]::NewGuid().ToString('N').Substring(0, 12)
$verificationPassword = [Guid]::NewGuid().ToString('N')
$projectRoot = Split-Path -Parent $PSScriptRoot
$artifacts = Join-Path $projectRoot '.artifacts'
New-Item -ItemType Directory -Path $artifacts -Force | Out-Null
$started = $false
try {
    & docker run --detach --rm --name $verificationName --read-only --cap-drop ALL --security-opt no-new-privileges:true --tmpfs '/data:rw,noexec,nosuid,uid=10001,gid=10001,size=32m' --tmpfs '/tmp:rw,noexec,nosuid,size=16m' -e "ADMIN_PASSWORD=$verificationPassword" -p '127.0.0.1::8080' $Image | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Testcontaineren startet ikke.' }
    $started = $true
    $address = (& docker port $verificationName '8080/tcp').Trim()
    if ($LASTEXITCODE -ne 0 -or $address -notmatch '^127\.0\.0\.1:\d+$') { throw 'Fant ikke testporten.' }
    $origin = "http://$address"
    $ready = $false
    for ($i = 0; $i -lt 50; $i++) {
        try {
            $health = Invoke-RestMethod -Uri "$origin/healthz" -TimeoutSec 2
            if ($health.status -eq 'ok') { $ready = $true; break }
        } catch { Start-Sleep -Milliseconds 100 }
    }
    if (-not $ready) { throw 'Helsesjekken feilet.' }
    $basic = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("admin:$verificationPassword"))
    $admin = Invoke-WebRequest -UseBasicParsing -Uri "$origin/admin" -Headers @{ Authorization = "Basic $basic" }
    if ($admin.StatusCode -ne 200 -or $admin.Content -notmatch 'Familiens oversikt') { throw 'Kontrollpanelet kunne ikke lastes.' }
    $installer = Invoke-WebRequest -UseBasicParsing -Uri "$origin/downloads/install.ps1"
    if ($installer.Content -notmatch 'EnrollmentCode') { throw 'Installasjonsfilen mangler.' }
    $binaryPath = Join-Path $artifacts "$verificationName-client.exe"
    Invoke-WebRequest -UseBasicParsing -Uri "$origin/downloads/screengate-client.exe" -OutFile $binaryPath
    $checksum = (Invoke-WebRequest -UseBasicParsing -Uri "$origin/downloads/screengate-client.exe.sha256").Content.Trim().Split(' ')[0]
    if ((Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash -ne $checksum) { throw 'Klientens sjekksum stemmer ikke.' }
    $runtime = & docker inspect --format '{{.Config.User}} {{.HostConfig.ReadonlyRootfs}}' $verificationName
    if ($runtime -ne 'screengate true') { throw "Uventet runtime: $runtime" }
    Write-Host 'Containerkontroll bestatt: helsesjekk, innlogging, installasjon, Windows-binær og SHA-256, uten root og med skrivebeskyttet rot.'
} finally {
    if ($started) { & docker stop $verificationName | Out-Null }
}
