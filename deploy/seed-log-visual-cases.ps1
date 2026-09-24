[CmdletBinding()]
param(
    [string]$ContainerName = "controltower-mysql-test-mysql-1",
    [string]$SourceDatabase = "new_api_mock",
    [string]$Marker = "ct-visual-v1"
)

$ErrorActionPreference = "Stop"

if ($Marker -notmatch '^[A-Za-z0-9_-]+$') { throw "Marker may contain only letters, numbers, hyphens, and underscores" }
if ($SourceDatabase -notmatch '^[A-Za-z0-9_]+$') { throw "SourceDatabase may contain only letters, numbers, and underscores" }

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

function Invoke-SeedQuery {
    param([Parameter(Mandatory)][string]$Sql)
    $result = & docker.exe @mysqlArgs -N -B -e $Sql
    if ($LASTEXITCODE -ne 0) { throw "MySQL visual-data query failed with exit code $LASTEXITCODE" }
    return $result
}

function Invoke-SeedSql {
    param([Parameter(Mandatory)][string]$Sql)
    $Sql | & docker.exe @mysqlArgs
    if ($LASTEXITCODE -ne 0) { throw "MySQL visual-data seed failed with exit code $LASTEXITCODE" }
}

$streamRequest = "$Marker-stream"
$fallbackRequest = "$Marker-fallback"
$markerSql = $Marker.Replace("'", "''")
$streamRequestSql = $streamRequest.Replace("'", "''")
$fallbackRequestSql = $fallbackRequest.Replace("'", "''")
$streamCount = [int]((Invoke-SeedQuery "SELECT COUNT(*) FROM logs WHERE request_id='$streamRequestSql';") | Select-Object -First 1)
$fallbackCount = [int]((Invoke-SeedQuery "SELECT COUNT(*) FROM logs WHERE request_id='$fallbackRequestSql';") | Select-Object -First 1)

if ($streamCount -eq 1 -and $fallbackCount -eq 3) {
    Write-Host "Visual log cases already exist for marker '$Marker'."
    exit 0
}
if ($streamCount -ne 0 -or $fallbackCount -ne 0) {
    throw "Marker '$Marker' has partial visual data ($streamCount stream row, $fallbackCount fallback rows); no rows were changed."
}

$catalog = (Invoke-SeedQuery "SELECT (SELECT COUNT(*) FROM users WHERE id=1),(SELECT COUNT(*) FROM channels WHERE id IN (1,2,6));") | Select-Object -First 1
$catalogParts = $catalog -split "\t"
if ($catalogParts.Count -ne 2 -or [int]$catalogParts[0] -lt 1 -or [int]$catalogParts[1] -ne 3) {
    throw "The mock database must contain user 1 and channels 1, 2, and 6 before seeding."
}

$streamOther = '{"load_marker":"' + $markerSql + '","stream_status":{"status":"error","end_reason":"client_gone"}}'
$fallbackOther1 = '{"load_marker":"' + $markerSql + '","admin_info":{"use_channel":["1"],"fallback":true}}'
$fallbackOther2 = '{"load_marker":"' + $markerSql + '","admin_info":{"use_channel":["1","2"],"fallback":true}}'
$fallbackOther3 = '{"load_marker":"' + $markerSql + '","admin_info":{"use_channel":["1","2","6"],"fallback":true}}'

$sql = @"
START TRANSACTION;
INSERT INTO logs (
  user_id, created_at, type, content, username, token_name, model_name,
  quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id,
  channel_name, token_id, ``group``, ip, request_id, upstream_request_id, other
) VALUES
  (1,UNIX_TIMESTAMP(UTC_TIMESTAMP())-60,5,'status_code=503, mock fallback attempt 1 failed','mock-admin','visual-chain-token','mock-gpt-4o',0,100,0,10,0,1,'Mock OpenAI',1,'default','127.0.0.1','$fallbackRequestSql','${fallbackRequestSql}-up-1','$fallbackOther1'),
  (1,UNIX_TIMESTAMP(UTC_TIMESTAMP())-45,5,'status_code=503, mock fallback attempt 2 failed','mock-admin','visual-chain-token','mock-gpt-4o',0,100,0,10,0,2,'Mock Anthropic',1,'default','127.0.0.1','$fallbackRequestSql','${fallbackRequestSql}-up-2','$fallbackOther2'),
  (1,UNIX_TIMESTAMP(UTC_TIMESTAMP())-30,2,'','mock-admin','visual-chain-token','mock-gpt-4o',100,100,25,10,0,6,'Mock Failover',1,'default','127.0.0.1','$fallbackRequestSql','${fallbackRequestSql}-up-3','$fallbackOther3'),
  (1,UNIX_TIMESTAMP(UTC_TIMESTAMP())-15,5,'status_code=499, client_gone (mock stream error)','mock-admin','visual-stream-token','mock-gpt-4o',0,100,0,10,1,1,'Mock OpenAI',1,'default','127.0.0.1','$streamRequestSql','${streamRequestSql}-up-1','$streamOther');
COMMIT;
"@

Invoke-SeedSql $sql
$verify = Invoke-SeedQuery @"
SELECT request_id,type,channel_id,
       COALESCE(JSON_UNQUOTE(JSON_EXTRACT(other,'$.stream_status.status')),''),
       COALESCE(JSON_UNQUOTE(JSON_EXTRACT(other,'$.stream_status.end_reason')),'')
FROM logs
WHERE request_id IN ('$streamRequestSql','$fallbackRequestSql')
ORDER BY created_at,id;
"@
$verifiedStreamCount = [int]((Invoke-SeedQuery "SELECT COUNT(*) FROM logs WHERE request_id='$streamRequestSql';") | Select-Object -First 1)
$verifiedFallbackCount = [int]((Invoke-SeedQuery "SELECT COUNT(*) FROM logs WHERE request_id='$fallbackRequestSql';") | Select-Object -First 1)
if ($verifiedStreamCount -ne 1 -or $verifiedFallbackCount -ne 3) {
    throw "Visual data verification failed ($verifiedStreamCount stream row, $verifiedFallbackCount fallback rows)."
}

Write-Host "Seeded 1 streaming soft-error row and 3 fallback attempts for marker '$Marker':"
Write-Host ($verify -join [Environment]::NewLine)
