param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$ListenAddr = "127.0.0.1:18081",
    [string]$AgentToken = "local-agent-token",
    [string]$DashboardToken = "local-dashboard-token",
    [string]$AgentTokenPepper = "local-token-pepper",
    [int]$AggregationIntervalSeconds = 60,
    [int]$NotificationIntervalSeconds = 30
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $ConfigPath)) {
    throw "Config file not found: $ConfigPath"
}

. $ConfigPath

if ([string]::IsNullOrWhiteSpace($CTMySQLTestUser)) {
    $CTMySQLTestUser = $CTMySQLAdminUser
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestPassword)) {
    $CTMySQLTestPassword = $CTMySQLAdminPassword
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestPassword) -or $CTMySQLTestPassword -eq "REPLACE_WITH_LOCAL_MYSQL_PASSWORD") {
    throw "Fill MySQL password in $ConfigPath before starting the server"
}

$env:CT_SERVER_LISTEN_ADDR = $ListenAddr
$env:CT_PUBLIC_BASE_URL = "http://$ListenAddr"
$env:CT_DATABASE_DRIVER = "mysql"
$env:CT_DATABASE_DSN = "{0}:{1}@tcp({2}:{3})/{4}?parseTime=true&loc=UTC" -f $CTMySQLTestUser, $CTMySQLTestPassword, $CTMySQLHost, $CTMySQLPort, $CTMySQLTestDatabase
$env:CT_MIGRATION_PATH = "server/migrations/001_init.sql"
$env:CT_AGGREGATION_INTERVAL_SECONDS = [string]$AggregationIntervalSeconds
$env:CT_NOTIFICATION_INTERVAL_SECONDS = [string]$NotificationIntervalSeconds
$env:CT_AGENT_TOKEN = $AgentToken
$env:CT_DASHBOARD_TOKEN = $DashboardToken
$env:CT_AGENT_TOKEN_PEPPER = $AgentTokenPepper

Push-Location (Join-Path $PSScriptRoot "..")
try {
    Write-Host "Starting Control Tower Server on http://$ListenAddr"
    Write-Host "Dashboard token: $DashboardToken"
    Write-Host "Agent token: $AgentToken"
    Write-Host "MySQL DSN is set in-process and password is not printed."
    & "C:\Program Files\Go\bin\go.exe" run ./server/cmd/control-tower-server
    if ($LASTEXITCODE -ne 0) {
        throw "go run exited with code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

