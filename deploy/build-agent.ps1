param(
  [string]$GoExe = "C:\Program Files\Go\bin\go.exe",
  [string]$OutputDir = "dist",
  [string]$OutputName = "control-tower-agent.exe"
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Resolve-Path (Join-Path $ScriptDir "..")
$ResolvedOutputDir = Join-Path $ProjectDir $OutputDir
New-Item -ItemType Directory -Force -Path $ResolvedOutputDir | Out-Null

Push-Location $ProjectDir
try {
  if (-not (Test-Path $GoExe)) {
    $GoExe = "go"
  }
  $env:CGO_ENABLED = "0"
  $outputPath = Join-Path $ResolvedOutputDir $OutputName
  & $GoExe build -buildvcs=false -trimpath -ldflags "-s -w" -o $outputPath ./agent/cmd/control-tower-agent
  Write-Host "Built $outputPath"
}
finally {
  Pop-Location
}

