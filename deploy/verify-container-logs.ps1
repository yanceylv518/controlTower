param([switch]$RequireMySQL)
$ErrorActionPreference = 'Stop'
if ($RequireMySQL -and -not $env:CT_MYSQL_TEST_DSN) {
    throw 'Set CT_MYSQL_TEST_DSN to an isolated test database before release validation. Never use production.'
}
Push-Location (Join-Path $PSScriptRoot '..')
try {
    go test ./agent/internal/containerlogs ./server/internal/dashboard ./server/internal/mysqlstore ./server/internal/httpapi
    if ($LASTEXITCODE -ne 0) { throw 'Container log Go regression tests failed' }
    pnpm --dir webapp --filter @ct/desktop exec node --test tests/logTaskRefresh.test.mjs tests/logHistory.test.mjs tests/logLine.test.mjs tests/copyText.test.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Container log frontend regression tests failed' }
    pnpm --dir webapp typecheck
    if ($LASTEXITCODE -ne 0) { throw 'Frontend typecheck failed' }
    if (-not $env:CT_MYSQL_TEST_DSN) {
        Write-Warning 'CT_MYSQL_TEST_DSN absent: real MySQL integration tests skipped. This does not replace pre-release MySQL validation.'
    }
} finally { Pop-Location }
