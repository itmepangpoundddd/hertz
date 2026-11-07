param(
  [string]$HostName = "127.0.0.1",
  [int]$Port = 5004,
  [string]$Bitrate = "128k"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root "test.opus"

if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
  throw "ffmpeg is required"
}
if (-not (Test-Path $src)) {
  Write-Host "generating $src"
  ffmpeg -hide_banner -loglevel warning -y `
    -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=60" `
    -ac 2 -c:a libopus -b:a $Bitrate $src
}

ffmpeg -hide_banner -loglevel warning -re -stream_loop -1 -i $src `
  -c copy -payload_type 111 -f rtp "rtp://${HostName}:${Port}?pkt_size=1200"
