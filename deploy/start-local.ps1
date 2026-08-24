param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$DatabaseName = "control_tower_test",
    [string]$SecretKey = "control-tower-local-dev-secret-key-v1",
    [int]$ServerPort = 18081,
    [int]$WebPort = 5173
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$runtimeDir = Join-Path $root "local\runtime"
$serverScript = Join-Path $PSScriptRoot "start-server-local.ps1"

if (-not (Test-Path -LiteralPath $ConfigPath)) {
    throw "Local MySQL config not found: $ConfigPath. Copy deploy/mysql-test.config.example.ps1 to local/mysql-test.config.ps1 and fill the local credentials."
}

New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null

$serverOut = Join-Path $runtimeDir "server.out.log"
$serverErr = Join-Path $runtimeDir "server.err.log"
$webOut = Join-Path $runtimeDir "web.out.log"
$webErr = Join-Path $runtimeDir "web.err.log"

$serverArgs = @(
    "-NoProfile", "-ExecutionPolicy", "Bypass",
    "-File", $serverScript,
    "-ConfigPath", $ConfigPath,
    "-ListenAddr", "127.0.0.1:$ServerPort",
    "-DatabaseName", $DatabaseName,
    "-SecretKey", $SecretKey
)
$server = Start-Process powershell.exe -ArgumentList $serverArgs -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput $serverOut -RedirectStandardError $serverErr -PassThru

$oldTarget = $env:CT_DEV_API_TARGET
try {
    $env:CT_DEV_API_TARGET = "http://127.0.0.1:$ServerPort"
    $web = Start-Process pnpm.cmd -ArgumentList @("--dir", "webapp", "dev", "--", "--host", "127.0.0.1", "--port", "$WebPort") -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput $webOut -RedirectStandardError $webErr -PassThru
}
finally {
    $env:CT_DEV_API_TARGET = $oldTarget
}

Set-Content -LiteralPath (Join-Path $runtimeDir "server.pid") -Value $server.Id
Set-Content -LiteralPath (Join-Path $runtimeDir "web.pid") -Value $web.Id

Write-Host "Control Tower local environment is starting."
Write-Host "Web:    http://127.0.0.1:$WebPort"
Write-Host "Server: http://127.0.0.1:$ServerPort"
Write-Host "MySQL database: $DatabaseName"
Write-Host "Logs: $runtimeDir"
