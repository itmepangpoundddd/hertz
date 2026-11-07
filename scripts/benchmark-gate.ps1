param(
  [string]$Url = "http://127.0.0.1:8080",
  [string]$Stream = "main",
  [string]$Token = "",
  [string]$Secret = "",
  [int]$Listeners = 1500,
  [int]$Ramp = 50,
  [string]$Duration = "10m",
  [string]$Loadgen = ""
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $Loadgen) { $Loadgen = Join-Path $root "bin\loadgen.exe" }
if (-not (Test-Path $Loadgen)) { throw "loadgen not found: $Loadgen" }
if (-not $Token -and -not $Secret) { throw "provide -Token or -Secret" }

$loadArgs = @(
  "-url", $Url, "-stream", $Stream,
  "-listeners", "$Listeners", "-ramp", "$Ramp", "-duration", $Duration
)
if ($Secret) { $loadArgs += @("-secret", $Secret, "-sub-prefix", "gate") }
else { $loadArgs += @("-token", $Token) }

$log = Join-Path $root ("benchmark-gate-" + (Get-Date -Format "yyyyMMdd-HHmmss") + ".log")
Write-Host "loadgen: $Loadgen"
Write-Host "log: $log"
& $Loadgen @loadArgs 2>&1 | Tee-Object -FilePath $log
if ($LASTEXITCODE -ne 0) { throw "loadgen failed with exit code $LASTEXITCODE" }

$summaryLine = Get-Content $log | Where-Object { $_ -like "SUMMARY *" } | Select-Object -Last 1
if (-not $summaryLine) { throw "SUMMARY line not found in $log" }
$summary = $summaryLine.Substring(8) | ConvertFrom-Json
$summary | ConvertTo-Json -Depth 4

$fail = @()
if ([double]$summary.join_success -lt 0.99) { $fail += "join_success < 0.99" }
if ([double]$summary.first_packet_p95_ms -ge 1000) { $fail += "first_packet_p95_ms >= 1000" }
if ($fail.Count -gt 0) {
  Write-Host ("GATE FAIL: " + ($fail -join "; ")) -ForegroundColor Red
  exit 1
}
Write-Host "LOAD-SIDE GATE PASS (join >=99%, first RTP p95 <1s). CPU gate is separate." -ForegroundColor Green
Write-Host "CPU check: rate(process_cpu_seconds_total[1m]) / GOMAXPROCS < 0.70 and node CPU <70%."
Write-Host "Also verify 192 kbps source, 1,500 actually joined, and zero/acceptable loss."
