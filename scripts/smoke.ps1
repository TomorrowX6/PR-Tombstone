param(
    [int]$ServerPort = 8080,
    [int]$WebPort = 5173
)

$ErrorActionPreference = "Stop"
$env:SERVER_PORT = [string]$ServerPort
$env:WEB_PORT = [string]$WebPort
$env:PUBLIC_BASE_URL = "http://localhost:$WebPort"

try {
    docker compose up -d --build
    if ($LASTEXITCODE -ne 0) { throw "docker compose up failed with exit code $LASTEXITCODE" }
    $deadline = (Get-Date).AddMinutes(3)
    do {
        Start-Sleep -Seconds 2
        try {
            $ready = Invoke-RestMethod -Uri "http://localhost:$ServerPort/readyz" -TimeoutSec 3
        } catch {
            $ready = $null
        }
    } while (-not $ready -and (Get-Date) -lt $deadline)
    if (-not $ready) { throw "API readiness deadline exceeded" }

    $body = '{}'
    $hmac = [System.Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes('TOKEN'))
    try { $digest = $hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes($body)) } finally { $hmac.Dispose() }
    $signature = 'sha256=' + ([BitConverter]::ToString($digest).Replace('-', '').ToLowerInvariant())
    $headers = @{ 'X-GitHub-Event' = 'ping'; 'X-GitHub-Delivery' = 'smoke-delivery'; 'X-Hub-Signature-256' = $signature }
    Invoke-RestMethod -Method Post -ContentType 'application/json' -Body $body -Headers $headers -Uri "http://localhost:$ServerPort/api/github/webhook" | Out-Null
    $duplicate = Invoke-RestMethod -Method Post -ContentType 'application/json' -Body $body -Headers $headers -Uri "http://localhost:$ServerPort/api/github/webhook"
    if (-not $duplicate.duplicate) { throw "webhook idempotency failed" }

    go run ./cmd/fixture
    $repositories = Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/repositories"
    if ($repositories.repositories.Count -lt 1) { throw "fixture repository missing" }
    $repositoryID = $repositories.repositories[0].id
    $tombstones = Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/tombstones/repository/$repositoryID"
    if ($tombstones.tombstones.Count -lt 1) { throw "fixture tombstone missing" }
    $tombstoneID = $tombstones.tombstones[0].id
    $state = Invoke-RestMethod -Method Put -ContentType "application/json" -Body '{"state":"SUPERSEDED"}' -Uri "http://localhost:$ServerPort/api/tombstones/$tombstoneID/state"
    if ($state.state -ne "SUPERSEDED") { throw "state transition failed" }
    Invoke-RestMethod -Method Put -ContentType "application/json" -Body '{"state":"ACTIVE"}' -Uri "http://localhost:$ServerPort/api/tombstones/$tombstoneID/state" | Out-Null
    Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/repositories/$repositoryID/settings" | Out-Null
    Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/repositories/$repositoryID/history" | Out-Null
    Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/jobs" | Out-Null
    Invoke-RestMethod -Uri "http://localhost:$ServerPort/api/graph/repository/$repositoryID" | Out-Null
    $databaseCheck = docker compose exec -T postgres psql -U postgres -d pr_tombstone -tAc "SELECT (SELECT COUNT(*) FROM schema_migrations) || ',' || (SELECT COUNT(*) FROM tombstone_claims) || ',' || (SELECT COUNT(*) FROM pull_requests WHERE snapshot ? 'evidence' OR snapshot::text LIKE '%`"patch`"%') || ',' || (SELECT COUNT(*) FROM evidence_items WHERE type='diff' AND body<>'');"
    if ($LASTEXITCODE -ne 0) { throw "database verification failed" }
    $values = $databaseCheck.Trim().Split(',')
    if ([int]$values[0] -lt 6 -or [int]$values[1] -lt 1 -or [int]$values[2] -ne 0 -or [int]$values[3] -ne 0) { throw "migration, claim, or diff-redaction verification failed: $databaseCheck" }
    curl.exe --fail --silent "http://localhost:$ServerPort/metrics" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "metrics endpoint failed" }
    curl.exe --fail --silent "http://localhost:$WebPort" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "web endpoint failed" }
    Write-Host "Smoke test passed."
} finally {
    docker compose down
}
