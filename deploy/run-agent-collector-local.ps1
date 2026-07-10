param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$ServerURL = "http://127.0.0.1:18081",
    [string]$AgentToken = "local-agent-token",
    [string]$AgentID = "local-agent-collector-1",
    [string]$InstanceID = "local-instance-collector-1",
    [string]$SourceDatabase = "control_tower_agent_source_test",
    [switch]$ResetState
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
    throw "Fill MySQL password in $ConfigPath before running this script"
}

Push-Location (Join-Path $PSScriptRoot "..")
try {
    $dataDir = "local\agent-collector-data"
    if ($ResetState -and (Test-Path -LiteralPath $dataDir)) {
        Remove-Item -LiteralPath $dataDir -Recurse -Force
    }

    $env:CT_AGENT_ID = $AgentID
    $env:CT_INSTANCE_ID = $InstanceID
    $env:CT_SERVER_URL = $ServerURL
    $env:CT_AGENT_TOKEN = $AgentToken
    $env:CT_LOG_DSN = "{0}:{1}@tcp({2}:{3})/{4}?parseTime=true&loc=UTC" -f $CTMySQLTestUser, $CTMySQLTestPassword, $CTMySQLHost, $CTMySQLPort, $SourceDatabase
    $env:CT_DATA_DIR = $dataDir
    $env:CT_LOG_BATCH_SIZE = "100"
    $env:CT_LOG_QUERY_TIMEOUT_SECONDS = "5"
    $env:CT_REPORT_TIMEOUT_SECONDS = "5"
    $env:CT_AGENT_FAKE_EVENT = "0"
    $env:CT_AGENT_RUN_ONCE = "1"

    Write-Host "Running one real logs collector pass against $SourceDatabase and reporting to $ServerURL"
    Write-Host "MySQL DSN is set in-process and password is not printed."
    & "C:\Program Files\Go\bin\go.exe" run ./agent/cmd/control-tower-agent
    if ($LASTEXITCODE -ne 0) {
        throw "go run exited with code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

