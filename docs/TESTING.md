# HERTZ Testing Guide

This guide is the operational test checklist for HERTZ. It separates **what was actually run** from **what still must be measured**. Do not mark a gate as passed unless every criterion in that gate was observed in this environment.

Environment used for the results quoted here: Windows + Intel Core i7-3770, Go 1.27.3, Pion v4.2.26, MinGW-w64 GCC 16.2.0 (portable). The SFU and load generator ran on the same machine for smoke tests unless stated otherwise; that is not a valid capacity benchmark.

---

## 1. Unit tests and race detector

### Standard commands

```bash
gofmt -w ./cmd ./internal
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

`go test -race` requires cgo and a C compiler. If `go env CGO_ENABLED` is `0` or `gcc` is missing:

#### Windows (method used here)

Download a portable MinGW-w64 build (for example WinLibs), then in PowerShell:

```powershell
$env:PATH = "C:\tools\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
gcc --version
go test -race ./... -count=1
```

Do not install MinGW system-wide just for this; a portable directory is enough.

#### WSL2 / Linux

```bash
sudo apt-get update
sudo apt-get install -y build-essential
CGO_ENABLED=1 go test -race ./... -count=1
```

#### Docker

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27-bookworm \
  sh -c 'apt-get update && apt-get install -y gcc && CGO_ENABLED=1 go test -race ./... -count=1'
```

### Opt-in shared-payload race test

The experimental shared-payload path is not enabled by default. To exercise it with two real Pion PeerConnections (TWCC/NACK/SRTP):

```bash
HERTZ_RACE_SHARED_PAYLOAD=1 CGO_ENABLED=1 go test -race ./internal/sfu \
  -run TestSharedPayloadRace -count=1 -v
```

Pass: `PASS`. The test skips without the environment variable; a skip is not evidence.

### What the unit suite covers

- jitter buffer: reorder, startup reorder, loss, late, duplicate, 16-bit sequence wrap, timestamp wrap, source restart
- ingest: payload type, SSRC pin, UDP IP:port pin, restart after 2 s, UDP read path
- SFU: per-listener seq/ts rewrite, ABR hysteresis, admission limits, remove-before-registration, PeerStats JSON in milliseconds
- signaling: audio-only/recvonly SDP, stream resolution, XFF trusted-hop selection, TURN REST credentials, static TURN credential rejection
- auth: token sign/verify/tamper/expiry/wildcard dev claims

---

## 2. Browser smoke test (Chrome / Safari / Firefox)

Start HERTZ with a known token and a source. Then serve the single-file page:

```bash
python3 -m http.server 8000 --directory web
# open http://127.0.0.1:8000/test.html
```

On Windows without Python, use any static file server, or open `web/test.html` directly. If opened as `file://`, the browser may block CORS/fetch; a local HTTP server is preferred.

1. Paste the API base (default `http://127.0.0.1:8080`).
2. Paste a short-lived token from `cmd/token`.
3. Click **Connect**.
4. Expected: audio element starts; state shows `subscribed: <id>`; session stats update every 2 s with `state`, `target_bps`, `rtt_ms`, `jitter_ms`, `loss_ratio`, `outbound_bps`, `down_track`.
5. Click **Disconnect**; expected: DELETE /v1/sessions/{id}, audio stops, state returns to `idle`.

Pass criteria:
- `pc.connectionState` becomes `connected` in all three browsers.
- Audio is audible and continuous for at least 60 s.
- No console errors, no repeated 401/403/429.
- `GET /v1/sessions/{id}` returns 200 for the owner and 403 for another subject.

This page has not yet been run against real Chrome/Safari/Firefox in this workspace. A browser pass must be recorded manually with browser version, OS, and date.

---

## 3. End-to-end acoustic latency

`first_packet_p50_ms` from loadgen is **not** end-to-end audio latency. Measure the real path:

### Option A: click/tone alignment (simplest)

1. Generate a source with a sharp click every 5 s, e.g. a short 1 kHz burst, and publish it as Opus/RTP.
2. Place a microphone next to the source speaker and another microphone/recorder at the listener output. Record both channels simultaneously with the same recorder (one stereo file).
3. In Audacity or `ffmpeg`, find the source click and the listener click in the same file.
4. Convert sample offset to milliseconds:

```text
latency_ms = (listener_click_sample - source_click_sample) / sample_rate * 1000
```

Run at least 30 clicks; report p50, p95, p99. Pass criterion: p95 < 500 ms. Record the jitter window, queue size, browser, OS, network RTT, and whether the source/listener were on the same machine.

### Option B: timestamp instrumentation

Send a known RTP header extension or an audio watermark containing a source wall-clock timestamp, capture the listener output, and compare against the same synchronized clock (PTP/NTP). Clock synchronization error must be subtracted. This is future work; no such instrumentation exists in HERTZ v1.

### Option C: loopback sanity check (not a substitute)

If source and listener are on the same machine, loopback removes network delay; a low number does not prove the <500 ms target over Wi-Fi/WAN. Label it as loopback-only.

---

## 4. `cmd/loadgen` reference

Build:

```bash
go build -o bin/loadgen ./cmd/loadgen
```

| Flag | Default | Meaning |
|---|---:|---|
| `-url` | `http://127.0.0.1:8080` | HERTZ signaling base URL |
| `-stream` | `main` | stream id |
| `-token` | — | one subscriber token shared by all listeners |
| `-secret` | — | HMAC secret; issues a unique token per listener |
| `-sub-prefix` | `load` | subject prefix when `-secret` is used |
| `-listeners` | `100` | number of headless Pion subscribers |
| `-ramp` | `20` | listeners started per second |
| `-duration` | `1m` | time each joined listener stays connected |
| `-join-timeout` | `20s` | per-listener HTTP/ICE timeout |
| `-insecure` | false | skip TLS verification for HTTPS signaling |

Per-second JSON output fields:

- `t`: elapsed time
- `attempted`: listeners started
- `joined`: successful SDP answers
- `failed`: join failures
- `active`: currently connected listeners
- `pps`: aggregate received RTP packets in the last interval
- `rx_bps`: aggregate received payload+header bits per second
- `total_pkts`: cumulative received packets
- `loss_ratio`: `lost / (lost + received)` from sequence gaps
- `duplicates`: duplicate sequence numbers
- `reordered`: out-of-order sequence numbers

Final `SUMMARY` fields: `join_success`, `first_packet_p50_ms`, `first_packet_p95_ms`, `first_packet_p99_ms`, `packets`, `bytes`, `loss_ratio`, `duplicates`, `reordered`, `duration`.

Important: the load generator runs in the same process for all listeners. For 1,000+ listeners, split across processes/hosts:

```bash
./bin/loadgen -url http://SFU:8080 -stream main -token "$TOKEN" \
  -listeners 500 -ramp 25 -duration 10m -sub-prefix gate-a
./bin/loadgen -url http://SFU:8080 -stream main -token "$TOKEN" \
  -listeners 500 -ramp 25 -duration 10m -sub-prefix gate-b
```

Set `HERTZ_MAX_SESSIONS_PER_IP=0` for a single-source load test; in production keep the cap. `HERTZ_JOIN_RATE_PER_SECOND`/`HERTZ_IP_JOIN_RATE_PER_SECOND` must be raised deliberately for the gate, and that change must be recorded.

---

## 5. CPU and memory measurement

### CPU

Define the criterion precisely. HERTZ exposes `process_cpu_seconds_total`; `GOMAXPROCS` is visible in `go_sched_gomaxprocs_threads`. A useful per-process CPU fraction is:

```promql
rate(process_cpu_seconds_total{job="hertz"}[1m])
/
go_sched_gomaxprocs_threads{job="hertz"}
```

This is **cores used / available Go CPUs**, not host CPU percent. For host CPU use `node_exporter`:

```promql
100 * (1 - avg(rate(node_cpu_seconds_total{job="node",mode="idle"}[1m])))
```

Pass criterion for the gate: the chosen definition < 70% for the steady-state window, with the exact PromQL and scrape interval recorded.

### Memory

```bash
HERTZ_PPROF=true ./bin/hertz
go tool pprof -top -nodecount=20 http://127.0.0.1:9090/debug/pprof/heap
go tool pprof -top -nodecount=20 http://127.0.0.1:9090/debug/pprof/heap?seconds=30
```

Also scrape `go_memstats_heap_alloc_bytes`, `go_memstats_heap_inuse_bytes`, `process_resident_memory_bytes`, and `go_goroutines`. Take a baseline before joins, a sample after the ramp, and one at the end of steady state. "No leak" means heap in use and goroutines return to baseline after sessions close, not merely that the process did not crash.

The repository includes a peer-object benchmark:

```bash
HERTZ_BENCH_PEERS=500 go test ./internal/sfu -run '^$' \
  -bench BenchmarkPeerGetStats -benchtime=1x -benchmem -v
HERTZ_BENCH_PEERS=1000 HERTZ_HEAP_PROFILE=/tmp/hertz-1000.heap \
  go test ./internal/sfu -run '^$' -bench BenchmarkPeerGetStats -benchtime=1x -benchmem -v
go tool pprof -top /tmp/hertz-1000.heap
```

This measures **unconnected Peer objects**, not live ICE/DTLS sessions. Use it as a lower bound and label it as such.

Live loopback measurements on the 2026-11-07 test host (client and server on the same machine):

| Sessions | Goroutines | heap_alloc | RSS | pprof in-use | NACK copy buffers |
|---:|---:|---:|---:|---:|---:|
| 500 | 12,041 | 882 MB | 1.10 GB | 510 MB | 385 MB |
| 1,000 | 24,078 | 1.44 GB | 1.84 GB at sample; 3.06 GB after fill | 664 MB | 448 MB |

The NACK responder's default packet cache dominates. Loss was ~3.3% at 500 and ~32% at 1,000 on this shared host, so neither run is a capacity pass. Record live measurements separately; do not substitute the 32.7 KB unconnected-peer figure.

---

## 6. Using `deploy/` and `scripts/`

`docker-compose.yml` starts HERTZ and Prometheus:

```bash
docker compose up --build
# signaling: http://127.0.0.1:8080
# metrics:    http://127.0.0.1:9090/metrics
# Prometheus: http://127.0.0.1:9091
```

The compose file uses `HERTZ_PUBLIC_IP=127.0.0.1`, `HERTZ_ICE_LOOPBACK=false`, and insecure dev auth for a local demo. Change the public IP, set a real `HERTZ_AUTH_SECRET`, and disable dev auth before exposing it.

Publisher helpers:

```bash
./scripts/publish.sh 127.0.0.1 5004 128k
```

or PowerShell:

```powershell
.\scripts\publish.ps1 -HostName 127.0.0.1 -Port 5004 -Bitrate 128k
```

Load helper:

```bash
LISTENERS=100 RAMP=20 DURATION=60s ./scripts/loadtest.sh
```

```powershell
.\scripts\loadtest.ps1 -Listeners 100 -Ramp 20 -Duration 60s
```

The publisher must produce Opus 48 kHz stereo, 20 ms frames, PT 111. FFmpeg's `-c copy` from an Opus file preserves the frame duration; verify with `ffprobe` if in doubt.

### TURN REST smoke test

Run coturn with a shared secret (example only; use a real secret):

```bash
docker run --rm -p 3478:3478/udp -p 3478:3478/tcp coturn/coturn \
  -n --log-file=stdout --realm=hertz.test \
  --use-auth-secret --static-auth-secret=0123456789abcdef0123456789abcdef \
  --no-tls --no-dtls
```

Configure HERTZ with the same secret and `HERTZ_TURN_URLS=turn:127.0.0.1:3478?transport=udp`. Fetch `/v1/config` with a token and verify the response contains a `turn:` entry with `username` of the form `<expiry>:<subject>` and a non-empty `credential`. Then force a browser behind a restrictive NAT or use `iceTransportPolicy: "relay"` in a test client and confirm audio still connects. Do not put the static secret in `HERTZ_BROWSER_ICE_SERVERS`; HERTZ rejects that.

---

## 7. Fault injection

Run the SFU and source on Linux or a Linux VM/WSL2 for `tc`. On Windows, use a Linux VM or a network emulator; do not claim results from an unrun `tc` command.

Baseline command for each case:

```bash
LISTENERS=100 RAMP=20 DURATION=5m ./scripts/loadtest.sh
```

### Loss 1%, 3%, 5%

```bash
sudo tc qdisc add dev eth0 root netem loss 1%
# run load test; then
sudo tc qdisc del dev eth0 root
```

Repeat with 3% and 5%. Expected: some sequence gaps; NACK repairs what the SFU still has; Opus FEC/PLC conceals the rest. Pass: listener stays connected, no massive reconnect storm, loss in `hertz_listener_*` roughly follows the injected loss, audio remains intelligible. Record whether the loss is on the source→SFU side or SFU→listener side; they exercise different recovery paths.

### RTT 50/100/200 ms

```bash
sudo tc qdisc add dev eth0 root netem delay 50ms 10ms
```

Repeat with 100/200 ms. Expected: first RTP and RTT increase; browser jitter buffer absorbs some; p95 acoustic latency must remain <500 ms for the gate. Measure acoustically, not with loadgen's first packet.

### Reorder

```bash
sudo tc qdisc add dev eth0 root netem reorder 1% 25%
```

Expected: ingest jitter buffer absorbs reordering; `hertz_ingest_late_packets_total` may rise; no out-of-order bursts at listeners.

### Source restart

Stop the publisher for >2 s, restart it with a new SSRC/seq/timestamp. Expected: HERTZ adopts the new source, resets the jitter buffer, and every DownTrack's `RTPRewriter` keeps listener seq/ts continuous. Verify with `GET /v1/sessions/{id}` / audio continuity. A restart inside 2 s is rejected by design.

### ICE restart / network change

From the browser page or client, create an offer with `{ iceRestart: true }` and POST it to `/v1/sessions/{id}/renegotiate`. Expected: 200 with a new answer, same session ID, audio recovers. Test by disabling/re-enabling Wi-Fi or changing IP.

### Token forgery/expiry

```bash
curl -i http://127.0.0.1:8080/v1/config -H 'Authorization: Bearer forged'
curl -i http://127.0.0.1:8080/v1/config -H 'Authorization: Bearer <expired-token>'
```

Expected: 401. Also test a token for stream A against stream B: 403.

### Video / oversized SDP / join flood

- POST an audio+video SDP → 400, no PeerConnection allocated.
- POST >`HERTZ_MAX_SDP_BYTES` body → 400/413, no panic.
- Send many concurrent `/v1/subscribe` from one IP → 429 after per-IP limiter/cap.
- Send malformed RTP (wrong PT/SSRC, oversized datagram, second IP:port) → rejected, counters increase, no crash.

---

## 8. Benchmark gate (1,500 listeners @ 192 kbps)

This gate has **not** been run. Do not claim it passed.

Requirements:
- SFU and load generator on separate machines; load generator must not share CPU/NIC with the SFU.
- Source: 192 kbps Opus, 48 kHz stereo, 20 ms, PT 111. Confirm the actual source bitrate from `hertz_ingest_bitrate_bps`.
- 1,500 listeners actually joined (`hertz_sessions_active`), not merely attempted.
- p95 join < 1 s.
- Steady-state CPU < 70% under the definition recorded in §5.
- No OOM, fd exhaustion, or unbounded goroutine growth. Record loss, RTT, jitter, and queue drops.

Scripts:

```powershell
# On the load-generator host, with bin\loadgen.exe built there
.\scripts\benchmark-gate.ps1 -Url http://SFU:8080 -Secret $env:HERTZ_AUTH_SECRET `
  -Listeners 1500 -Ramp 50 -Duration 10m
```

```bash
# Linux load-generator host (chmod +x once if needed)
chmod +x scripts/benchmark-gate.sh
URL=http://SFU:8080 SECRET="$HERTZ_AUTH_SECRET" LISTENERS=1500 RAMP=50 DURATION=10m \
  ./scripts/benchmark-gate.sh
```

The scripts check only the load-side criteria (`join_success >= 0.99`, `first_packet_p95_ms < 1000`) and print the CPU PromQL reminder. They cannot prove CPU <70% or that the source is 192 kbps; those must be captured from Prometheus and the source metrics.

For 1,500 listeners on one load host, shard the generator with three explicit processes:

```bash
./bin/loadgen -url http://SFU:8080 -stream main -token "$TOKEN" \
  -listeners 500 -ramp 20 -duration 10m -sub-prefix gate-0 &
./bin/loadgen -url http://SFU:8080 -stream main -token "$TOKEN" \
  -listeners 500 -ramp 20 -duration 10m -sub-prefix gate-1 &
./bin/loadgen -url http://SFU:8080 -stream main -token "$TOKEN" \
  -listeners 500 -ramp 20 -duration 10m -sub-prefix gate-2 &
wait
```

Three processes on one host still share that host's CPU/NIC; a valid capacity gate should use multiple load hosts where practical.

Record the final table:

```text
Date:
SFU host / CPU / NIC:
Load host(s):
Browser or loadgen version:
Source bitrate (measured):
Listeners joined / attempted:
p50/p95/p99 first RTP:
Join p95:
SFU CPU (definition):
Node CPU:
Loss p95:
RTT p95:
Jitter p95:
Queue drops:
Heap/goroutines before/after:
Gate result: PASS / FAIL / INCOMPLETE
```

If any field is missing, the gate is **INCOMPLETE**, not passed.
