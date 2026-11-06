# Run from the project root; reads schema without modifying data.
$ErrorActionPreference = 'Stop'
$script = Join-Path $PSScriptRoot 'verify-baseline.sql'
Get-Content -Raw -Encoding UTF8 $script |
  docker compose --env-file .env -f deploy/compose.yaml exec -T postgres `
    psql -X -v ON_ERROR_STOP=1 -U montage_app -d montage -1
if ($LASTEXITCODE -ne 0) { throw "Baseline verification failed (exit $LASTEXITCODE)" }
Write-Host 'Schema verification passed. No migration was applied.'
