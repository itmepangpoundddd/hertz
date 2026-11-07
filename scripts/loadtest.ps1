param(
  [int]$Listeners = 100,
  [int]$Ramp = 20,
  [string]$Duration = "60s",
  [string]$Url = "http://127.0.0.1:8080",
  [string]$Stream = "main",
  [string]$Token = "dev"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
& (Join-Path $root "bin\loadgen.exe") `
  -url $Url -stream $Stream -token $Token `
  -listeners $Listeners -ramp $Ramp -duration $Duration
