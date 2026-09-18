[CmdletBinding()]
param([string]$Image = 'screengate:verification')

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$testProject = 'screengate-upgrade-' + [Guid]::NewGuid().ToString('N').Substring(0, 12)
$testVolume = $testProject + '_screengate-data'
$composeArgs = @('compose', '--project-name', $testProject, '--env-file', (Join-Path $projectRoot '.env.example'),
    '-f', (Join-Path $projectRoot 'docker-compose.yml'), '-f', (Join-Path $PSScriptRoot 'compose.smoke.yml'))
$testEnvironment = @{
    ADMIN_PASSWORD = [Guid]::NewGuid().ToString('N'); ADMIN_USER = 'admin'; ADMIN_PATH = '/admin'
    SCREENGATE_BIND = '127.0.0.1'; SCREENGATE_PORT = '0'; SCREENGATE_TIMEZONE = 'Europe/Oslo'
    SCREENGATE_TEST_IMAGE = $Image
}
$savedEnvironment = @{}

function Invoke-TestCompose {
    $result = & docker @composeArgs @args
    if ($LASTEXITCODE -ne 0) { throw 'Test Compose command failed.' }
    return $result
}

function Invoke-VolumeHelper([string]$Script) {
    $result = & docker run --rm --network none --read-only --user '0:0' --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --security-opt no-new-privileges:true --mount "type=volume,src=$testVolume,dst=/data" --entrypoint /bin/sh alpine:3.21 -ec $Script
    if ($LASTEXITCODE -ne 0) { throw 'Isolated test volume check failed.' }
    return $result
}

function Get-TestOrigin {
    $address = (Invoke-TestCompose port screengate '8080').Trim()
    if ($address -notmatch '^127\.0\.0\.1:\d+$') { throw 'Unexpected test address.' }
    $origin = "http://$address"
    for ($attempt = 0; $attempt -lt 50; $attempt++) {
        try {
            if ((Invoke-RestMethod -Uri "$origin/healthz" -TimeoutSec 2).status -eq 'ok') { return $origin }
        } catch { }
        Start-Sleep -Milliseconds 200
    }
    throw 'Test server health check failed.'
}

function Get-TestAdmin([string]$Origin) {
    return Invoke-WebRequest -UseBasicParsing -Uri "$Origin/admin" -Headers $authHeaders -TimeoutSec 5
}

function Set-TestQuota([string]$Origin, [string]$Minutes) {
    $page = Get-TestAdmin $Origin
    $token = [regex]::Match($page.Content, 'name="csrf_token" value="([^"]+)"').Groups[1].Value
    if (-not $token) { throw 'Missing CSRF token.' }
    # Windows PowerShell drops Authorization when following the successful 303.
    $response = Invoke-WebRequest -UseBasicParsing -Uri "$Origin/admin/user-quota" -Method Post -Headers $authHeaders -Body @{
        csrf_token = $token; user = 'upgrade-fixture'; hours = '1'; minutes = $Minutes
    } -TimeoutSec 5 -MaximumRedirection 0 -ErrorAction SilentlyContinue
    if ($response.StatusCode -ne 303) { throw 'Saving the test quota failed.' }
}

function Assert-TestQuota([string]$Origin, [string]$Minutes) {
    $content = (Get-TestAdmin $Origin).Content
    if ($content -notmatch 'upgrade-fixture' -or $content -notmatch ('name="minutes"[^>]*value="' + $Minutes + '"')) {
        throw 'Existing user quota was not preserved.'
    }
}

try {
    foreach ($key in $testEnvironment.Keys) {
        $savedEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $testEnvironment[$key], 'Process')
    }
    $basic = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes('admin:' + $testEnvironment.ADMIN_PASSWORD))
    $authHeaders = @{ Authorization = "Basic $basic" }

    # Fresh volume: the dependency must prepare it before the non-root server starts.
    Invoke-TestCompose up -d --no-build | Out-Null
    $origin = Get-TestOrigin
    Set-TestQuota $origin '37'
    Assert-TestQuota $origin '37'
    Invoke-TestCompose stop screengate | Out-Null

    # Reproduce a legacy database, including restrictive directory/sidecar modes.
    Invoke-VolumeHelper 'touch /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal /data/unrelated; chown 0:0 /data /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal /data/unrelated; chmod 700 /data; chmod 600 /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal; stat -c %u:%g /data/screengate.db' | Out-Null
    & docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true --mount "type=volume,src=$testVolume,dst=/data" --entrypoint /bin/sh $Image -ec 'test ! -w /data && test ! -w /data/screengate.db'
    if ($LASTEXITCODE -ne 0) { throw 'The legacy fixture did not reproduce missing write access.' }
    $before = Invoke-VolumeHelper 'sha256sum /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal'
    Invoke-TestCompose up --force-recreate --no-build --exit-code-from data-permissions data-permissions | Out-Null
    $after = Invoke-VolumeHelper 'sha256sum /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal'
    if (($before -join "`n") -ne ($after -join "`n")) { throw 'Permission preparation changed database contents.' }
    $owners = @(Invoke-VolumeHelper 'stat -c %u:%g /data /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal /data/unrelated')
    if (($owners -join ',') -ne '10001:10001,10001:10001,10001:10001,10001:10001,10001:10001,0:0') {
        throw 'Unexpected ownership or unrelated files were changed.'
    }

    # Repeat from root ownership through the actual full Compose startup path.
    Invoke-VolumeHelper 'chown 0:0 /data /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm /data/screengate.db-journal' | Out-Null
    Invoke-TestCompose up -d --force-recreate --no-build | Out-Null
    $origin = Get-TestOrigin
    Assert-TestQuota $origin '37'
    Set-TestQuota $origin '38'
    Assert-TestQuota $origin '38'
    $serverID = (Invoke-TestCompose ps -q screengate).Trim()
    $runtime = & docker inspect --format '{{.Config.User}} {{.HostConfig.ReadonlyRootfs}}' $serverID
    if ($LASTEXITCODE -ne 0 -or $runtime -ne 'screengate true') { throw 'Server no longer runs non-root with read-only root.' }
    $uid = & docker exec $serverID id -u
    if ($LASTEXITCODE -ne 0 -or $uid -ne '10001') { throw 'Unexpected server UID.' }

    # Re-running the initializer on an already migrated volume must be harmless.
    Invoke-TestCompose stop screengate | Out-Null
    Invoke-TestCompose up -d --force-recreate --no-build | Out-Null
    Assert-TestQuota (Get-TestOrigin) '38'
    Invoke-TestCompose stop screengate | Out-Null

    # An unexpected symlink must fail closed and prevent the application starting.
    Invoke-VolumeHelper 'if [ -e /data/screengate.db-journal ]; then mv /data/screengate.db-journal /data/fixture-journal; fi; ln -s /data/unrelated /data/screengate.db-journal' | Out-Null
    & docker @composeArgs up -d --force-recreate --no-build | Out-Null
    if ($LASTEXITCODE -eq 0) { throw 'A symlink was incorrectly accepted.' }
    $running = @(Invoke-TestCompose ps --status running -q screengate)
    if ($running.Count -ne 0) { throw 'Server started despite failed volume preparation.' }
    $sentinelOwner = Invoke-VolumeHelper 'stat -c %u:%g /data/unrelated'
    if ($sentinelOwner -ne '0:0') { throw 'Symlink target ownership was changed.' }
    Write-Host 'Upgrade checks passed: fresh volume, legacy root ownership, SQLite sidecars, unchanged contents, preserved/writable quotas, repeat startup, non-root runtime and symlink rejection.'
} finally {
    # Only this randomly named test project's resources are removed, never real data.
    if ($testProject -notmatch '^screengate-upgrade-[0-9a-f]{12}$') { throw 'Unsafe cleanup target.' }
    & docker @composeArgs down --volumes --remove-orphans | Out-Null
    $cleanupFailed = $LASTEXITCODE -ne 0
    foreach ($key in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($key, $savedEnvironment[$key], 'Process')
    }
    if ($cleanupFailed) { Write-Warning "Could not remove all resources for test project $testProject." }
}
