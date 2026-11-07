#!/usr/bin/env bash
set -euo pipefail

# Example: LISTENERS=1000 RAMP=50 DURATION=10m ./scripts/loadtest.sh
LISTENERS="${LISTENERS:-100}"
RAMP="${RAMP:-20}"
DURATION="${DURATION:-60s}"
URL="${URL:-http://127.0.0.1:8080}"
STREAM="${STREAM:-main}"
TOKEN="${TOKEN:-dev}"

exec "$(dirname "$0")/../bin/loadgen" \
  -url "$URL" -stream "$STREAM" -token "$TOKEN" \
  -listeners "$LISTENERS" -ramp "$RAMP" -duration "$DURATION"
