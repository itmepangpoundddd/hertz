#!/usr/bin/env bash
set -euo pipefail

# Run this on a host separate from the SFU. The source must already publish
# 192 kbps Opus. CPU must be checked separately with Prometheus/pprof.
URL="${URL:-http://127.0.0.1:8080}"
STREAM="${STREAM:-main}"
TOKEN="${TOKEN:-}"
SECRET="${SECRET:-}"
LISTENERS="${LISTENERS:-1500}"
RAMP="${RAMP:-50}"
DURATION="${DURATION:-10m}"
LOADGEN="${LOADGEN:-$(cd "$(dirname "$0")/.." && pwd)/bin/loadgen}"

if [[ -z "$TOKEN" && -z "$SECRET" ]]; then
  echo "provide TOKEN or SECRET" >&2
  exit 2
fi
if [[ ! -x "$LOADGEN" ]]; then
  echo "loadgen not executable: $LOADGEN" >&2
  exit 2
fi

LOG="benchmark-gate-$(date +%Y%m%d-%H%M%S).log"
ARGS=(-url "$URL" -stream "$STREAM" -listeners "$LISTENERS" -ramp "$RAMP" -duration "$DURATION")
if [[ -n "$SECRET" ]]; then
  ARGS+=(-secret "$SECRET" -sub-prefix gate)
else
  ARGS+=(-token "$TOKEN")
fi

echo "log: $LOG"
set +e
"$LOADGEN" "${ARGS[@]}" 2>&1 | tee "$LOG"
rc=${PIPESTATUS[0]}
set -e
if [[ $rc -ne 0 ]]; then
  echo "loadgen failed with $rc" >&2
  exit "$rc"
fi

SUMMARY="$(grep '^SUMMARY ' "$LOG" | tail -1 | sed 's/^SUMMARY //')"
echo "$SUMMARY"
JOIN="$(printf '%s' "$SUMMARY" | sed -n 's/.*"join_success":\([0-9.]*\).*/\1/p')"
P95="$(printf '%s' "$SUMMARY" | sed -n 's/.*"first_packet_p95_ms":\([0-9.]*\).*/\1/p')"

fail=0
awk -v v="${JOIN:-0}" 'BEGIN{exit !(v < 0.99)}' && { echo "GATE FAIL: join_success < 0.99"; fail=1; }
awk -v v="${P95:-999999}" 'BEGIN{exit !(v >= 1000)}' && { echo "GATE FAIL: first_packet_p95_ms >= 1000"; fail=1; }
if [[ $fail -ne 0 ]]; then exit 1; fi

echo "LOAD-SIDE GATE PASS (join >=99%, first RTP p95 <1s). CPU gate is separate."
echo "CPU: rate(process_cpu_seconds_total[1m]) / GOMAXPROCS < 0.70 and node CPU <70%."
echo "Verify 192 kbps source, 1,500 joined, and acceptable loss from the SFU metrics."
