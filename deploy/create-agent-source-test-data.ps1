param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$MySQLExe = "C:\Program Files\MySQL\MySQL Server 9.7\bin\mysql.exe",
    [string]$SourceDatabase = "control_tower_agent_source_test",
    [string]$RequestID = "local-real-collector-request"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $ConfigPath)) {
    throw "Config file not found: $ConfigPath"
}
if (-not (Test-Path -LiteralPath $MySQLExe)) {
    throw "mysql.exe not found: $MySQLExe"
}

. $ConfigPath

if ([string]::IsNullOrWhiteSpace($CTMySQLAdminPassword) -or $CTMySQLAdminPassword -eq "REPLACE_WITH_LOCAL_MYSQL_PASSWORD") {
    throw "Fill CTMySQLAdminPassword in $ConfigPath before running this script"
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestUser)) {
    $CTMySQLTestUser = $CTMySQLAdminUser
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestPassword)) {
    $CTMySQLTestPassword = $CTMySQLAdminPassword
}

$dbName = $SourceDatabase.Replace('`', '``')
$request = $RequestID.Replace("'", "''")
$createdAt = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$sourceLogID = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$sql = @"
CREATE DATABASE IF NOT EXISTS ``$dbName`` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE ``$dbName``;
CREATE TABLE IF NOT EXISTS logs (
  id BIGINT NOT NULL PRIMARY KEY,
  created_at BIGINT NOT NULL,
  type INT NOT NULL,
  content TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(255) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  token_id BIGINT NOT NULL,
  token_name VARCHAR(255) NOT NULL,
  prompt_tokens BIGINT NOT NULL,
  completion_tokens BIGINT NOT NULL,
  quota BIGINT NOT NULL,
  use_time BIGINT NOT NULL,
  is_stream TINYINT(1) NOT NULL,
  ``group`` VARCHAR(128) NOT NULL,
  request_id VARCHAR(255) NOT NULL,
  upstream_request_id VARCHAR(255) NOT NULL,
  other TEXT NOT NULL,
  INDEX idx_logs_id_type (id, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS channels (
  id BIGINT NOT NULL PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  status INT NOT NULL DEFAULT 1,
  weight BIGINT NOT NULL DEFAULT 0,
  models TEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO channels (id, name, status, weight, models) VALUES
  (2002, 'local-primary-channel', 1, 80, 'gpt-4o,claude-3-5-sonnet,control-tower-real-model'),
  (2003, 'local-backup-channel', 2, 20, 'gpt-4o-mini,control-tower-real-model')
ON DUPLICATE KEY UPDATE name = VALUES(name), status = VALUES(status), weight = VALUES(weight), models = VALUES(models);
DELETE FROM logs;
INSERT INTO logs (
  id, created_at, type, content, user_id, username, channel_id, model_name,
  token_id, token_name, prompt_tokens, completion_tokens, quota, use_time,
  is_stream, ``group``, request_id, upstream_request_id, other
) VALUES (
  $sourceLogID, $createdAt, 2, '', 1002, 'local-real-user', 2002, 'control-tower-real-model',
  3002, 'local-real-token', 21, 34, 110, 2,
  1, 'local-real', '$request', 'local-real-upstream', '{"cache_tokens":13}'
)
ON DUPLICATE KEY UPDATE request_id = VALUES(request_id);
"@

$oldMySQLPwd = $env:MYSQL_PWD
try {
    $env:MYSQL_PWD = $CTMySQLAdminPassword
    $sql | & $MySQLExe --protocol=tcp -h $CTMySQLHost -P $CTMySQLPort -u $CTMySQLAdminUser --default-character-set=utf8mb4
    if ($LASTEXITCODE -ne 0) {
        throw "mysql.exe exited with code $LASTEXITCODE"
    }
}
finally {
    $env:MYSQL_PWD = $oldMySQLPwd
}

Write-Host "Created or verified source logs database: $SourceDatabase"
Write-Host "Inserted source log request id: $RequestID"
Write-Host "Source log DSN can be built from local config. Password is not printed."

