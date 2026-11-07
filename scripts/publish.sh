#!/usr/bin/env bash
set -euo pipefail

# Publish a looped 48 kHz stereo Opus test tone as raw RTP.
# Usage: ./scripts/publish.sh [host] [port] [bitrate]
HOST="${1:-127.0.0.1}"
PORT="${2:-5004}"
BITRATE="${3:-128k}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/test.opus"

if ! command -v ffmpeg >/dev/null 2>&1; then
  echo "ffmpeg is required" >&2
  exit 1
fi

if [[ ! -f "$SRC" ]]; then
  echo "generating $SRC"
  ffmpeg -hide_banner -loglevel warning -y \
    -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=60" \
    -ac 2 -c:a libopus -b:a "$BITRATE" "$SRC"
fi

exec ffmpeg -hide_banner -loglevel warning -re -stream_loop -1 -i "$SRC" \
  -c copy -payload_type 111 -f rtp "rtp://${HOST}:${PORT}?pkt_size=1200"
