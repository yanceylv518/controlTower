[CmdletBinding()]
param(
    [string]$ContainerName = "controltower-mysql-test-mysql-1",
    [string]$SourceDatabase = "new_api_mock",
    [int]$Rows = 1000000,
    [int]$BatchSize = 100000,
    [string]$Marker = "ct-perf-v1",
    [switch]$Reset
)

$ErrorActionPreference = "Stop"

if ($Rows -le 0) { throw "Rows must be positive" }
if ($BatchSize -lt 1000 -or $BatchSize -gt 100000) { throw "BatchSize must be between 1000 and 100000" }
if ($Marker -notmatch '^[A-Za-z0-9_-]+$') { throw "Marker may contain only letters, numbers, hyphens, and underscores" }

# 从容器环境读取密码，避免把凭据写入仓库或输出到终端。
$envLines = & docker.exe inspect $ContainerName --format '{{range .Config.Env}}{{println .}}{{end}}'
if ($LASTEXITCODE -ne 0) { throw "Cannot inspect MySQL container: $ContainerName" }
$passwordLine = $envLines | Where-Object { $_ -like "MYSQL_ROOT_PASSWORD=*" } | Select-Object -First 1
$rootPassword = if ($passwordLine) { $passwordLine.Substring("MYSQL_ROOT_PASSWORD=".Length) } else { "" }
if ([string]::IsNullOrWhiteSpace($rootPassword)) { throw "MYSQL_ROOT_PASSWORD is not available in the container environment" }

$mysqlArgs = @(
    "exec", "-i", "-e", "MYSQL_PWD=$rootPassword", $ContainerName,
    "mysql", "--protocol=socket", "-uroot", $SourceDatabase,
    "--default-character-set=utf8mb4"
)

function Invoke-SeedSql {
    param([Parameter(Mandatory)][string]$Sql)
    $Sql | & docker.exe @mysqlArgs
    if ($LASTEXITCODE -ne 0) { throw "MySQL seed statement failed with exit code $LASTEXITCODE" }
}

$markerSql = $Marker.Replace("'", "''")
$existingSql = "SELECT COUNT(*) FROM logs WHERE request_id LIKE '$markerSql-%';"
$existing = (& docker.exe @mysqlArgs -N -B -e $existingSql | Select-Object -First 1).ToString().Trim()
if ($LASTEXITCODE -ne 0) { throw "Unable to inspect existing seeded logs" }
$existingCount = 0L
if (-not [Int64]::TryParse($existing, [ref]$existingCount)) { throw "Unexpected existing-row count returned by MySQL: $existing" }

if ($Reset -and $existingCount -gt 0) {
    Write-Host "Removing $existingCount existing rows with marker '$Marker'."
    Invoke-SeedSql "DELETE FROM logs WHERE request_id LIKE '$markerSql-%';"
    $existingCount = 0
}
elseif ($existingCount -gt 0) {
    if ($existingCount -ge $Rows) {
        Write-Host "Marker '$Marker' already contains $existingCount rows; nothing to do. Use -Reset to recreate it."
        exit 0
    }
    throw "Marker '$Marker' already contains $existingCount rows. Use -Reset to recreate the dataset."
}

$digits = @"
SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9
"@
$numbers = @"
SELECT a.n + 10*b.n + 100*c.n + 1000*d.n + 10000*e.n AS n
FROM ($digits) a
CROSS JOIN ($digits) b
CROSS JOIN ($digits) c
CROSS JOIN ($digits) d
CROSS JOIN ($digits) e
"@

$inserted = 0L
while ($inserted -lt $Rows) {
    $batchRows = [Math]::Min($BatchSize, $Rows - $inserted)
    $row = "(n + $inserted)"
    $statusCode = "CASE MOD(FLOOR($row / 20), 5) WHEN 0 THEN 429 WHEN 1 THEN 413 WHEN 2 THEN 503 WHEN 3 THEN 400 ELSE 502 END"
    $type = "IF(MOD($row, 20) < 4, 5, 2)"
    $content = "IF(MOD($row, 20) < 4, CONCAT('status_code=', $statusCode, ', synthetic load error'), '')"
    $other = "CONCAT('{`"load_marker`":`"$markerSql`",`"status_code`":', IF(MOD($row, 20) < 4, $statusCode, 0), '}')"

    $sql = @"
SET @base_epoch = UNIX_TIMESTAMP(UTC_TIMESTAMP()) - 31 * 86400;
INSERT INTO logs (
  user_id, created_at, type, content, username, token_name, model_name,
  quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id,
  channel_name, token_id, ``group``, ip, request_id, upstream_request_id, other
)
SELECT
  1000 + MOD($row, 250),
  @base_epoch + MOD($row * 7919, 2678400),
  $type,
  $content,
  CONCAT('load-user-', LPAD(1 + MOD($row, 250), 3, '0')),
  CONCAT('load-token-', LPAD(1 + MOD($row, 40), 2, '0')),
  CONCAT('load-model-', LPAD(1 + MOD($row, 20), 2, '0')),
  IF(MOD($row, 20) < 4, 0, 100 + MOD($row, 900)),
  200 + MOD($row, 1800),
  IF(MOD($row, 20) < 6, 0, 100 + MOD($row, 1600)),
  1 + MOD($row, 60),
  MOD($row, 3) = 0,
  1 + MOD($row, 20),
  CONCAT('Load Channel ', 1 + MOD($row, 20)),
  5000 + MOD($row, 40),
  CASE MOD($row, 5) WHEN 0 THEN 'default' WHEN 1 THEN 'vip' WHEN 2 THEN 'standard' WHEN 3 THEN 'enterprise' ELSE 'trial' END,
  CONCAT('10.20.', MOD($row, 250), '.', 1 + MOD($row, 250)),
  CONCAT('$markerSql-', LPAD($row, 7, '0')),
  CONCAT('$markerSql-up-', LPAD($row, 7, '0')),
  $other
FROM (
$numbers
) AS numbers
WHERE n < $batchRows;
"@

    Write-Host ("Inserting rows {0:N0}-{1:N0} of {2:N0}..." -f ($inserted + 1), ($inserted + $batchRows), $Rows)
    Invoke-SeedSql $sql
    $inserted += $batchRows
}

$verifySql = @"
SELECT
  COUNT(*) AS total_rows,
  SUM(type = 2 AND completion_tokens > 0) AS normal_rows,
  SUM(type = 2 AND completion_tokens = 0) AS empty_output_rows,
  SUM(type = 5 AND content LIKE 'status_code=429,%') AS status_429_rows,
  SUM(type = 5 AND content LIKE 'status_code=413,%') AS status_413_rows,
  SUM(type = 5 AND content LIKE 'status_code=503,%') AS status_503_rows,
  SUM(type = 5 AND content LIKE 'status_code=400,%') AS status_400_rows,
  SUM(type = 5 AND content LIKE 'status_code=502,%') AS status_502_rows,
  FROM_UNIXTIME(MIN(created_at)), FROM_UNIXTIME(MAX(created_at))
FROM logs
WHERE request_id LIKE '$markerSql-%';
"@
$verify = & docker.exe @mysqlArgs -N -B -e $verifySql
if ($LASTEXITCODE -ne 0) { throw "Unable to verify seeded logs" }
Write-Host "Seed complete for marker '$Marker':"
Write-Host ($verify -join [Environment]::NewLine)
