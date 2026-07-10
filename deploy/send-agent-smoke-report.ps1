param(
    [string]$ServerURL = "http://127.0.0.1:18081",
    [string]$AgentToken = "local-agent-token",
    [string]$AgentID = "local-agent-1",
    [string]$InstanceID = "local-instance-1"
)

$ErrorActionPreference = "Stop"

Push-Location (Join-Path $PSScriptRoot "..")
try {
    $env:CT_AGENT_ID = $AgentID
    $env:CT_INSTANCE_ID = $InstanceID
    $env:CT_SERVER_URL = $ServerURL
    $env:CT_AGENT_TOKEN = $AgentToken
    $env:CT_LOG_DSN = "local-smoke-unused"
    $env:CT_DATA_DIR = "local\agent-data"
    $env:CT_REPORT_TIMEOUT_SECONDS = "5"
    $env:CT_AGENT_FAKE_EVENT = "1"

    Write-Host "Sending one local Control Tower Agent smoke report to $ServerURL"
    & "C:\Program Files\Go\bin\go.exe" run ./agent/cmd/control-tower-agent
    if ($LASTEXITCODE -ne 0) {
        throw "go run exited with code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}
