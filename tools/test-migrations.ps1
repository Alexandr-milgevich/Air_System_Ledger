# Stage 11 migration smoke test. Run from repository root, Docker must be running.
# Creates a uniquely named TEMPORARY test database, tests up/down/up, drops it.
$ErrorActionPreference = 'Stop'
$db = 'montage_stage11_test_' + ([guid]::NewGuid().ToString('N').Substring(0,12))
$created = $false
function PsqlFile([string]$path,[string]$database) {
    Get-Content -Raw -Encoding UTF8 $path |
      docker compose --env-file .env -f deploy/compose.yaml exec -T postgres `
        psql -X -v ON_ERROR_STOP=1 -U montage_app -d $database -1
    if ($LASTEXITCODE -ne 0) { throw "SQL failed: $path" }
}
function MigrationFile([int]$index,[string]$kind) {
    $pattern = ('{0:D6}_*.{1}.sql' -f $index, $kind)
    $files = @(Get-ChildItem .\migrations -Filter $pattern)
    if ($files.Count -ne 1) { throw "Expected one file matching $pattern, got $($files.Count)" }
    return $files[0].FullName
}
try {
    docker compose --env-file .env -f deploy/compose.yaml exec -T postgres createdb -U montage_app $db
    if ($LASTEXITCODE -ne 0) { throw 'Unable to create a disposable test database' }
    $created = $true
    Write-Host "Disposable database created: $db"
    foreach ($i in 1..5) { PsqlFile (MigrationFile $i 'up') $db }
    Write-Host 'Up migrations: passed'
    foreach ($i in 5..1) { PsqlFile (MigrationFile $i 'down') $db }
    Write-Host 'Down migrations: passed'
    foreach ($i in 1..5) { PsqlFile (MigrationFile $i 'up') $db }
    Write-Host 'Second up migrations: passed'
    Write-Host 'PASS: migration up/down/up on disposable database'
}
finally {
    if ($created) {
        docker compose --env-file .env -f deploy/compose.yaml exec -T postgres dropdb --if-exists --force -U montage_app $db
        if ($LASTEXITCODE -ne 0) { Write-Warning "Cleanup failed: manually remove test database $db" }
    }
}
