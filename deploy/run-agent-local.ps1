param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$SourceDatabase = "control_tower_agent_source_test",
    [string]$ServerURL = "http://127.0.0.1:18081",
    [string]$AgentToken = "local-agent-token",
    [string]$AgentID = "local-agent-1",
    [string]$InstanceID = "local-new-api-1",
    [string]$DataDir = (Join-Path $PSScriptRoot "..\local\agent-data"),
    [switch]$RunOnce = $true
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
    throw "Fill MySQL password in $ConfigPath before running the agent"
}

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

$env:CT_AGENT_ID = $AgentID
$env:CT_INSTANCE_ID = $InstanceID
$env:CT_SERVER_URL = $ServerURL
$env:CT_AGENT_TOKEN = $AgentToken
$env:CT_LOG_DSN = "{0}:{1}@tcp({2}:{3})/{4}?parseTime=true&loc=UTC" -f $CTMySQLTestUser, $CTMySQLTestPassword, $CTMySQLHost, $CTMySQLPort, $SourceDatabase
$env:CT_DATA_DIR = $DataDir
$env:CT_AGENT_RUN_ONCE = [string]$RunOnce.IsPresent
$env:CT_DOCKER_ENABLED = "false"
$env:CT_CHANNEL_SNAPSHOT_ENABLED = "true"
$env:CT_CHANNEL_SNAPSHOT_LIMIT = "1000"

Push-Location (Join-Path $PSScriptRoot "..")
try {
    Write-Host "Running Control Tower Agent once against $ServerURL"
    Write-Host "Source database: $SourceDatabase"
    Write-Host "MySQL DSN is set in-process and password is not printed."
    & "C:\Program Files\Go\bin\go.exe" run ./agent/cmd/control-tower-agent
    if ($LASTEXITCODE -ne 0) {
        throw "go run exited with code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}