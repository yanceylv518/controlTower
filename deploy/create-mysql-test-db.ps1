param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\local\mysql-test.config.ps1"),
    [string]$MySQLExe = "C:\Program Files\MySQL\MySQL Server 9.7\bin\mysql.exe",
    [switch]$RunIntegrationTest
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $ConfigPath)) {
    throw "Config file not found: $ConfigPath"
}
if (-not (Test-Path -LiteralPath $MySQLExe)) {
    throw "mysql.exe not found: $MySQLExe"
}

. $ConfigPath

if ([string]::IsNullOrWhiteSpace($CTMySQLAdminUser)) {
    throw "CTMySQLAdminUser is required in $ConfigPath"
}
if ([string]::IsNullOrWhiteSpace($CTMySQLAdminPassword) -or $CTMySQLAdminPassword -eq "REPLACE_WITH_LOCAL_MYSQL_PASSWORD") {
    throw "Fill CTMySQLAdminPassword in $ConfigPath before running this script"
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestDatabase)) {
    throw "CTMySQLTestDatabase is required in $ConfigPath"
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestUser)) {
    $CTMySQLTestUser = $CTMySQLAdminUser
}
if ([string]::IsNullOrWhiteSpace($CTMySQLTestPassword)) {
    $CTMySQLTestPassword = $CTMySQLAdminPassword
}

$databaseName = $CTMySQLTestDatabase.Replace('`', '``')
$sql = "CREATE DATABASE IF NOT EXISTS ``$databaseName`` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

$oldMySQLPwd = $env:MYSQL_PWD
try {
    $env:MYSQL_PWD = $CTMySQLAdminPassword
    & $MySQLExe --protocol=tcp -h $CTMySQLHost -P $CTMySQLPort -u $CTMySQLAdminUser --default-character-set=utf8mb4 -e $sql
    if ($LASTEXITCODE -ne 0) {
        throw "mysql.exe exited with code $LASTEXITCODE"
    }
}
finally {
    $env:MYSQL_PWD = $oldMySQLPwd
}

$dsn = "{0}:{1}@tcp({2}:{3})/{4}?parseTime=true&loc=UTC" -f $CTMySQLTestUser, $CTMySQLTestPassword, $CTMySQLHost, $CTMySQLPort, $CTMySQLTestDatabase
Write-Host "Created or verified MySQL database: $CTMySQLTestDatabase"
Write-Host "Integration-test DSN is ready in this process as CT_MYSQL_TEST_DSN. Password is not printed."

if ($RunIntegrationTest) {
    $oldTestDSN = $env:CT_MYSQL_TEST_DSN
    try {
        $env:CT_MYSQL_TEST_DSN = $dsn
        Push-Location (Join-Path $PSScriptRoot "..")
        try {
            & "C:\Program Files\Go\bin\go.exe" test ./server/internal/mysqlstore
            if ($LASTEXITCODE -ne 0) {
                throw "go test exited with code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }
    }
    finally {
        $env:CT_MYSQL_TEST_DSN = $oldTestDSN
    }
}

