# HERTZ

**High-efficiency Engine for RealTime Zero-delay streaming** — a low-latency, audio-only WebRTC SFU written in Go with Pion.

HERTZ replaces TCP/HTTP streaming (Icecast-style) with Opus over WebRTC/UDP. It ingests raw RTP from a hardware mixer or microphone host, reorders it once, and selectively forwards the same Opus packets to many browser listeners over DTLS-SRTP.

This repository is a working reference implementation, not a finished SaaS. The data plane, signaling, auth, metrics, and a load generator are functional and were exercised end-to-end with FFmpeg as the publisher. Start with **[docs/TUTORIAL.md](docs/TUTORIAL.md)** for a Thai step-by-step walkthrough, and see **[docs/TESTING.md](docs/TESTING.md)** for the test matrix, browser page, acoustic latency method, fault injection, and the benchmark gate.

---

## 1. What is in the box

| Path | Purpose |
|---|---|
| `cmd/hertz` | HERTZ node binary: wires ingest, SFU, signaling, metrics, shutdown |
| `cmd/hertz-admin` | Windows one-click app: embedded engine, first-run setup UI, tray, listener links/QR |
| `cmd/loadgen` | Headless Pion listener load generator |
| `cmd/token` | Issues HMAC subscriber tokens for local testing |
| `internal/rtpengine` | UDP/Unix RTP ingest, validation, jitter buffer, stats |
| `internal/sfu` | Stream hub, per-listener down tracks, PeerConnections, GCC, ABR |
| `internal/signaling` | HTTP SDP offer/answer, session lifecycle, ICE restart |
| `internal/auth` | Stateless HMAC bearer tokens |
| `internal/metrics` | Prometheus collectors and histograms |
| `internal/config` | Environment configuration |
| `deploy/` | Docker, Compose, Prometheus scrape config |
| `web/test.html` | single-file browser listener/statistics smoke test |
| `docs/TUTORIAL.md` | Thai step-by-step tutorial from build to production checklist |
| `docs/ADMIN.md` | Thai guide for the Windows `HERTZ.exe` admin app |
| `docs/TESTING.md` | test matrix, fault injection, benchmark gate |
| `scripts/` | FFmpeg publisher and load-test helpers |

### Architecture

```text
 Publisher (mixer / mic host)
   │  Opus RTP, 48 kHz stereo, 20 ms ptime
   │  UDP :5004  or  unixgram /run/hertz/ingest.sock
   ▼
┌──────────────────────────────────────────────────────────────┐
│ INGEST                                                       │
│  UDP/Unix reader                                             │
│    → RTP validation (version, PT, SSRC pin, source ACL, PPS) │
│    → strip untrusted RTP header extensions                   │
│    → deep-copy packet                                        │
│    → 40 ms sequence-ordered jitter buffer                    │
│    → RFC 3550 jitter / loss / late / duplicate counters      │
└──────────────────────────────┬───────────────────────────────┘
                               │ one canonical RTP packet, 50 pps
                               ▼
┌──────────────────────────────────────────────────────────────┐
│ SFU / FANOUT                                                 │
│  Stream registry                                             │
│    → per-listener DownTrack                                  │
│    → bounded queue (10 packets), drop-oldest on overflow     │
│    → one writer goroutine per listener                       │
│    → Pion PeerConnection:                                    │
│         GCC bandwidth estimate + no-op/leaky pacer           │
│         TWCC extension, NACK responder                       │
│         SRTP encrypt, DTLS, ICE (single UDP mux port)        │
└──────────────────────────────┬───────────────────────────────┘
                               │ SRTP / UDP / ICE
                               ▼
                 Browser listeners (Opus decode)
```

Control plane:

```text
Browser ── HTTPS ──► POST /v1/subscribe      { SDP offer } + Bearer token
                    ◄─ 201 { session_id, SDP answer }
                    ── POST /v1/sessions/{id}/renegotiate  (ICE restart)
                    ── DELETE /v1/sessions/{id}
Prometheus ─────────► GET :9090/metrics
```

### End-to-end latency budget

| Stage | Typical | Notes |
|---|---:|---|
| Capture + Opus encode | 10–25 ms | source dependent |
| Source → SFU network | 1–30 ms | LAN < 2 ms |
| Ingest jitter buffer | 40 ms | `HERTZ_JITTER_WINDOW` |
| Fanout / queue | 0–20 ms | typical; queue worst case is 10 × 20 ms = 200 ms |
| SFU → listener network | 5–50 ms | one-way, RTT/2 |
| Opus algorithmic delay | 26.5 ms | 20 ms frame + 6.5 ms lookahead |
| Browser jitter buffer + decode | 30–100 ms | browser controlled |
| **Total (calculated)** | **~115–290 ms** | excluding a full output queue; target < 500 ms |

The values above are a calculation from the configured stages, not a measured end-to-end latency. A full per-listener queue can add up to ~200 ms before drop-oldest engages, which puts the absolute calculated worst case near 500 ms. The dominant tunables are the ingest jitter window and browser jitter buffer. 20 ms jitter is viable on a controlled LAN; 40–60 ms is safer on Wi-Fi/WAN. See `docs/TESTING.md` for a real acoustic latency measurement procedure; it has not been run yet.

---

## 2. Why Go + Pion, not Mediasoup

| Criterion | Go + Pion v4 | C++/Node + Mediasoup |
|---|---|---|
| Deployment | single static binary | Node runtime + C++ worker processes |
| Raw forwarding performance | good, but GC and per-PC goroutines | better, C++ workers per core |
| SFU features | you build the SFU; Pion gives ICE/DTLS/SRTP/RTP/RTCP, NACK, TWCC, GCC | mature router/transport, simulcast/SVC, congestion control |
| Audio-only fit | excellent: Opus passthrough, low CPU | excellent, but more machinery than needed |
| Development speed | high; type-safe, one language | control plane in JS/TS, data plane C++ |
| Observability | native Go pprof/Prometheus | Node + worker metrics across processes |
| Operational risk | Pion's per-peer model needs benchmarking | more moving parts, native addon builds |
| Scale ceiling per node | target 1–2k audio listeners; measure | generally higher |

**Decision: Go + Pion v4 for HERTZ v1.** The differentiator is not raw video SFU throughput; it is a custom RTP ingest, a small control plane, and a single binary that can be dropped next to a mixer. Audio at 128–256 kbps is a low-bitrate workload compared with video, so Pion's performance is sufficient if per-listener queues and a single ICE UDP port are used.

**Benchmark gate:** if one node cannot sustain **1,500 listeners at 192 kbps** with p95 join < 1 s and CPU < 70%, keep the Go control plane and replace the data plane with Mediasoup workers or a C++ relay. The architecture deliberately separates ingest, SFU, and signaling so this is a data-plane swap, not a rewrite.

---

## 3. Key design decisions and trade-offs

### 3.1 SFU, not MCU

HERTZ never decodes or re-encodes Opus. One canonical RTP stream is forwarded to every listener. This keeps CPU low and preserves source quality. The cost: per-listener quality adaptation cannot change the Opus bitrate without a transcoder. See ABR below.

### 3.2 Raw RTP ingest, not WHIP

Hardware mixers and audio hosts speak RTP over UDP or a Unix datagram socket. A dedicated socket per stream keeps the hot path free of stream lookup. WHIP is a future ingest path for authenticated remote publishers.

Raw RTP has **no cryptographic publisher authentication**. v1 relies on:
- source-IP CIDR allowlist (`HERTZ_INGEST_ALLOWED_CIDRS`),
- optional SSRC pinning (`HERTZ_INGEST_SSRC`) plus automatic UDP IP:port pinning; a new IP:port is accepted only when the SSRC also changes after 2 s of silence,
- private VLAN / WireGuard / host firewall,
- Unix socket mode `0600` for same-host mixers,
- payload-type and packet-rate validation.

Do not expose the ingest UDP port to the public Internet.

### 3.3 One jitter buffer at ingest, not one per listener

Reordering is a property of the source→SFU path. Repairing it once avoids per-listener buffering and keeps all listeners on the same sequence/timestamp timeline. The 40 ms window adds fixed latency; the alternative is audible glitches under reordering or much larger browser buffers.

Late packets are dropped, not reordered into the past. Loss is counted from sequence gaps and concealed by Opus PLC/FEC at the browser.

### 3.4 Per-listener down-track queue

Pion's `TrackLocalStaticRTP` writes to bound tracks from the broadcaster goroutine. With thousands of listeners, one slow SRTP/DTLS writer can stall the fanout. HERTZ inserts a bounded queue and one writer goroutine per listener:

- broadcaster only clones and enqueues (non-blocking),
- full queue drops the oldest packet (latency stays bounded),
- a sequence gap is preferable to a growing delay; Opus PLC conceals it,
- queue capacity is `HERTZ_QUEUE_SIZE` (default 10 × 20 ms = 200 ms worst case, normally near empty).

Trade-off: one packet copy and one goroutine per listener. At 50 pps with Opus packets of roughly 320–640 bytes this is manageable for audio; it is the first thing to optimize with refcounted buffers if CPU becomes the limit.

### 3.5 Per-listener sequence/timestamp rewrite, per-listener SSRC

Every `DownTrack` owns an `RTPRewriter`. It keeps sequence numbers and timestamps continuous for that listener when the source restarts, while preserving gaps caused by real packet loss. Pion rewrites SSRC and payload type per binding. NACK caches are per peer; a packet lost before the SFU, or dropped from that listener's bounded queue, still cannot be recovered by NACK — only Opus FEC/PLC can help.

### 3.6 GCC per listener + source ABR

Each subscriber gets its own Pion API and GCC estimator. The default pacer is **no-op**; `HERTZ_PACER=leaky` is opt-in. This is more expensive than sharing one estimator, but a shared estimate would let one bad Wi-Fi listener degrade every other listener.

`internal/sfu/abr.go` aggregates listener target bitrates and asks the source encoder for the **median** target via an optional webhook (`HERTZ_ENCODER_CONTROL_URL`). This is broadcast policy: protect the majority, not the worst single listener. The source encoder remains the only place bitrate can actually change without transcoding.

If per-listener tiers are required, add 2–3 transcoded Opus renditions (e.g. 64 kbps mono, 128 kbps stereo, 192 kbps stereo) and switch the listener's `DownTrack`/sender. That is intentionally out of scope for v1.

**Pacer policy:** the default `HERTZ_PACER=noop` forwards at the source's natural 20 ms pace. Pion's leaky-bucket pacer has an unbounded queue; if the target bitrate falls below the source bitrate, that queue converts congestion into latency, which is the wrong trade for live audio. GCC still estimates the path and drives source-side ABR; `HERTZ_PACER=leaky` is available for experiments, but monitor its queue. A future bounded token-bucket shaper can enforce a cap while dropping instead of queueing.

### 3.7 Single ICE UDP port

All PeerConnections share a `pion/ice` UDP mux on `HERTZ_ICE_UDP_PORT` (default 8443). This avoids a huge ephemeral port range and simplifies firewalls/NAT. `HERTZ_PUBLIC_IP` publishes a NAT 1:1 host candidate; no STUN is required on the server when it is set.

### 3.8 Non-trickle SDP

The browser sends a complete offer, HERTZ gathers ICE candidates and returns a complete answer. This is simple and works through ordinary HTTP load balancers. Cost: join waits for ICE gathering (typically < 200 ms with host candidates). Trickle ICE is a future optimization.

### 3.9 HMAC tokens, not a session database

Tokens are stateless and short-lived. The upstream application authenticates the user and signs `{sub, stream, kind, exp}`. HERTZ verifies signature, expiry, kind, and stream audience. This keeps the SFU horizontally scalable. Revocation is by short TTL; a one-time `jti` store is future work.

---

## 4. Quick start

Requirements: Go 1.26+, FFmpeg (for the test publisher), a browser or the included load generator.

```bash
# Build
go build -o bin/hertz ./cmd/hertz
go build -o bin/loadgen ./cmd/loadgen
go build -o bin/token ./cmd/token

# Run (development auth; never expose this to the Internet)
export HERTZ_ALLOW_INSECURE_DEV=true
export HERTZ_AUTH_SECRET=0123456789abcdef0123456789abcdef
export HERTZ_HTTP_ADDR=:8080
export HERTZ_METRICS_ADDR=:9090
export HERTZ_INGEST_ADDR=:5004
export HERTZ_ICE_UDP_PORT=8443
export HERTZ_ICE_LOOPBACK=true
export HERTZ_STREAM_ID=main
./bin/hertz
```

Publish a test tone with FFmpeg:

```bash
ffmpeg -hide_banner -re -f lavfi \
  -i "sine=frequency=440:sample_rate=48000:duration=600" \
  -ac 2 -c:a libopus -b:a 128k -frame_duration 20 -application lowdelay \
  -payload_type 111 -f rtp "rtp://127.0.0.1:5004?pkt_size=1200"
```

The FFmpeg SDP is printed on stderr; HERTZ ignores it because the source is raw RTP. The important parts are Opus/48000/2, payload type 111, and 20 ms frames.

Run one headless listener:

```bash
./bin/loadgen -url http://127.0.0.1:8080 -stream main \
  -listeners 1 -ramp 1 -duration 15s
```

Expected output includes `joined:1`, ~50 pps, and a received bitrate near the source bitrate.

### Windows one-click admin app

Operators who do not want to use environment variables can build a single `HERTZ.exe`:

```powershell
.\scripts\build-admin.ps1
# -> dist\HERTZ.exe

# If local scripts are blocked:
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-admin.ps1
```

First launch keeps a console visible, opens `http://127.0.0.1:8090/setup`, and starts the embedded engine after the form is saved. Later launches hide the console and live in the system tray. The admin UI is in English, generates listener links and QR codes plus an iframe embed snippet for listener websites, shows source/listener/bitrate status, exposes direct Ogg/Opus and public-metadata URLs for VLC/custom players, edits now-playing metadata, and exposes engine start/stop/restart and logs.

```text
%LOCALAPPDATA%\HERTZ\config.json
%LOCALAPPDATA%\HERTZ\engine\hertz-engine.exe
%LOCALAPPDATA%\HERTZ\logs\hertz.log
```

Latest local artifacts (2026-11-07):

| File | Bytes | SHA-256 |
|---|---:|---|
| `dist\HERTZ.exe` | 24,089,088 | `8189D8574D47781CB3FE3FDF400126D52AD3623EAAFC927B2710A53F0B589C37` |
| `dist\HERTZ-windows-x64.zip` | 9,745,791 | `31374A7322576E35D468283F531C33464A3E0615B3E0D85F1ACAF425EE195495` |

The admin listener is loopback-only and rejects foreign `Host`/`Origin` headers. See **[docs/ADMIN.md](docs/ADMIN.md)** for setup, firewall, tray, butt configuration, TURN, and troubleshooting.

### Direct audio for VLC and custom players

The admin dashboard shows two token-free links:

| Link | Purpose |
|---|---|
| `http://<host>:<http_port>/live.ogg` | Ogg/Opus HTTP stream for VLC and other HTTP players |
| `http://<host>:<http_port>/v1/public/status` | Public JSON: listener counts, source bitrate, now playing |

VLC: **Media → Open Network Stream** → paste the `/live.ogg` URL. HERTZ taps the same Opus packets that WebRTC listeners receive; it does not decode or re-encode them. Chrome and Firefox can also play Ogg/Opus in an `<audio>` element; Safari support varies, so keep `/listen` (WebRTC) for browser listeners. VLC's default network cache can delay the first sound by several seconds; lower it to about 200 ms if faster startup matters.

Example public metadata response:

```json
{
  "stream_id": "main",
  "online": true,
  "listeners": { "webrtc": 10, "http": 2, "total": 12 },
  "bitrate_bps": 132000,
  "target_bitrate_bps": 256000,
  "now_playing": {
    "title": "Song name",
    "artist": "Artist",
    "updated_at": "2026-11-07T15:40:00Z"
  },
  "links": {
    "audio_ogg": "/live.ogg",
    "webrtc": "/listen",
    "metadata": "/v1/public/status"
  }
}
```

Update now playing from a playout script with a short-lived publisher token:

```bash
PUB_TOKEN=$(./bin/token -kind pub -secret "$HERTZ_AUTH_SECRET" -stream main -ttl 1h)
curl -s -X POST http://127.0.0.1:8080/v1/nowplaying \
  -H "Authorization: Bearer $PUB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Song name","artist":"Artist"}'
```

The **Now playing** card in the admin dashboard does the same thing: the admin process signs a short-lived `KindPublish` token locally and forwards the request to the engine, so the token never reaches the browser. Title and artist are limited to 200 characters; control characters are rejected. The metadata is in memory and is cleared when the engine restarts.

HTTP audio listeners are capped by `HERTZ_MAX_HTTP_LISTENERS` (default 200). Each connection is a separate Ogg stream; slow clients drop packets rather than blocking WebRTC fanout.

### Production token flow

```bash
TOKEN=$(./bin/token -secret "$HERTZ_AUTH_SECRET" -sub listener-42 -stream main -ttl 15m)
curl -s http://127.0.0.1:8080/v1/config \
  -H "Authorization: Bearer $TOKEN" | jq
```

### Browser signaling

A browser creates a `recvonly` audio transceiver, creates an offer, waits for ICE gathering, POSTs the SDP, and applies the answer:

```js
const pc = new RTCPeerConnection({ iceServers });
pc.addTransceiver('audio', { direction: 'recvonly' });
pc.ontrack = (e) => { audio.srcObject = e.streams[0]; };

const offer = await pc.createOffer();
await pc.setLocalDescription(offer);
await new Promise(r => {
  if (pc.iceGatheringState === 'complete') return r();
  pc.addEventListener('icegatheringstatechange', () => {
    if (pc.iceGatheringState === 'complete') r();
  });
});

const res = await fetch('/v1/subscribe', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
  body: JSON.stringify({ stream_id: 'main', sdp: pc.localDescription.sdp }),
});
const { answer } = await res.json();
await pc.setRemoteDescription(answer);
```

For an ICE restart after a network change, create a new offer with `{ iceRestart: true }` and POST it to `/v1/sessions/{id}/renegotiate`.

---

## 5. HTTP API

Most `/v1/*` endpoints require `Authorization: Bearer <token>`; `GET /v1/public/status` is deliberately public and read-only, and `POST /v1/nowplaying` requires a publisher (`pub`) token. When `HERTZ_ALLOW_INSECURE_DEV=true`, the literal token `dev` is accepted and maps to any stream.

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/healthz` | — | `{status, sessions}` |
| GET | `/listen` | — | WebRTC listener page |
| GET | `/live.ogg` | — | Ogg/Opus HTTP audio stream |
| GET | `/v1/public/status` | — | listener counts, source bitrate, now playing |
| POST | `/v1/nowplaying` | `{title, artist}` | `{ok, now_playing}`; requires publisher token |
| GET | `/v1/config` | — | ICE servers, stream, codec, bitrate |
| POST | `/v1/subscribe` | `{stream_id, sdp}` | `201 {session_id, answer, ice_servers}` |
| GET | `/v1/sessions/{id}` | — | state, target bps, RTT, loss, queue stats |
| POST | `/v1/sessions/{id}/renegotiate` | `{sdp}` | `{session_id, answer}` |
| DELETE | `/v1/sessions/{id}` | — | `204` |

Errors: `400` bad SDP/JSON, `401` bad/expired token, `403` stream/subject mismatch, `404` unknown session, `429` capacity/per-IP/join-rate limit, `500` internal.

SDP is restricted to audio-only m-lines (`HERTZ_MAX_MEDIA_SECTIONS`). Video and data-channel offers are rejected before a PeerConnection is allocated.

---

## 6. Configuration

| Env | Default | Meaning |
|---|---|---|
| `HERTZ_HTTP_ADDR` | `:8080` | signaling listen address |
| `HERTZ_METRICS_ADDR` | `:9090` | Prometheus/pprof listen address |
| `HERTZ_STREAM_ID` | `main` | logical stream id |
| `HERTZ_INGEST_ADDR` | `:5004` | RTP UDP listen; empty disables |
| `HERTZ_INGEST_UNIX` | — | unixgram path; empty disables |
| `HERTZ_INGEST_PT` | `111` | expected Opus payload type; `0` accepts any |
| `HERTZ_INGEST_SSRC` | `0` | pin SSRC; `0` locks to first source |
| `HERTZ_INGEST_ALLOWED_CIDRS` | — | comma-separated source allowlist |
| `HERTZ_INGEST_MAX_PPS` | `500` | per-source packet-rate cap |
| `HERTZ_JITTER_WINDOW` | `40ms` | reorder/playout delay |
| `HERTZ_JITTER_TICK` | `5ms` | playout scheduler tick |
| `HERTZ_MAX_PACKET_SIZE` | `1500` | max UDP datagram |
| `HERTZ_UDP_READ_BUFFER` | `4194304` | kernel receive buffer |
| `HERTZ_ICE_UDP_PORT` | `8443` | shared ICE UDP mux port; `0` = ephemeral |
| `HERTZ_ICE_LOOPBACK` | `false` | include loopback candidates; set `true` only for local dev |
| `HERTZ_PUBLIC_IP` | — | NAT 1:1 public IP for host candidates |
| `HERTZ_ICE_SERVERS` | — | JSON array for the **server** ICE agent; may contain static TURN |
| `HERTZ_BROWSER_ICE_SERVERS` | — | listener-safe JSON array; static TURN credentials are rejected |
| `HERTZ_TURN_REST_SECRET` | — | coturn `static-auth-secret` for short-lived TURN credentials |
| `HERTZ_TURN_URLS` | — | comma-separated TURN URLs returned to listeners |
| `HERTZ_TURN_TTL` | `1h` | TURN REST credential lifetime |
| `HERTZ_INITIAL_BITRATE` | `256000` | GCC initial target |
| `HERTZ_MIN_BITRATE` | `96000` | GCC lower bound |
| `HERTZ_MAX_BITRATE` | `512000` | GCC upper bound and Opus fmtp cap |
| `HERTZ_PACER` | `noop` | `noop` keeps latency bounded; `leaky` shapes but queues without bound |
| `HERTZ_QUEUE_SIZE` | `10` | per-listener packets (~20 ms each; 200 ms worst case) |
| `HERTZ_STATS_INTERVAL` | `5s` | WebRTC stats sampling |
| `HERTZ_AUTH_SECRET` | — | HMAC secret, ≥ 16 bytes |
| `HERTZ_ALLOW_INSECURE_DEV` | `false` | accept token `dev` |
| `HERTZ_MAX_SESSIONS` | `5000` | global session cap |
| `HERTZ_MAX_SESSIONS_PER_IP` | `8` | per-IP cap; `0` unlimited |
| `HERTZ_MAX_HTTP_LISTENERS` | `200` | max concurrent `/live.ogg` listeners |
| `HERTZ_MAX_SDP_BYTES` | `262144` | request body cap |
| `HERTZ_MAX_MEDIA_SECTIONS` | `4` | audio-only m-line cap |
| `HERTZ_JOIN_RATE_PER_SECOND` | `50` | global join token bucket |
| `HERTZ_JOIN_BURST` | `100` | global join burst |
| `HERTZ_IP_JOIN_RATE_PER_SECOND` | `5` | per-IP join rate, checked before global |
| `HERTZ_IP_JOIN_BURST` | `10` | per-IP join burst |
| `HERTZ_SHARED_PAYLOAD` | `false` | experimental shared-payload fanout; see §8 |
| `HERTZ_ENCODER_CONTROL_URL` | — | optional source ABR webhook |
| `HERTZ_ENCODER_CONTROL_TOKEN` | — | bearer token for the ABR webhook |
| `HERTZ_ALLOWED_ORIGINS` | `*` | CORS allowlist |
| `HERTZ_TRUST_PROXY` | `false` | honor `X-Forwarded-For` |
| `HERTZ_TRUSTED_PROXY_HOPS` | `1` | number of trusted proxies; IP is taken from the right |
| `HERTZ_PPROF` | `false` | expose pprof on metrics listener |
| `HERTZ_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

`HERTZ_ICE_SERVERS` example (server-side agent):

```json
[{"urls":["stun:stun.example.net:3478"]},{"urls":["turn:turn.example.net:3478"],"username":"hertz","credential":"secret"}]
```

Listener/browser ICE configuration is separate. Put only STUN or non-secret entries in `HERTZ_BROWSER_ICE_SERVERS`; HERTZ rejects static TURN credentials there:

```json
[{"urls":["stun:stun.example.net:3478"]}]
```

For coturn with `use-auth-secret`, set:

```bash
HERTZ_TURN_REST_SECRET=<coturn static-auth-secret>
HERTZ_TURN_URLS=turn:turn.example.net:3478?transport=udp,turn:turn.example.net:3478?transport=tcp
HERTZ_TURN_TTL=1h
```

HERTZ then returns short-lived `username=<expiry>:<subject>` and `credential=base64(HMAC-SHA1(secret, username))` from `/v1/config` and `/v1/subscribe`. The static secret never leaves the server. See `docs/TESTING.md` for a coturn smoke test.

---

## 7. Observability

Scrape `:9090/metrics`. Key series:

- `hertz_ingest_packets_total`, `hertz_ingest_bytes_total`
- `hertz_ingest_bitrate_bps`, `hertz_ingest_jitter_seconds`
- `hertz_ingest_lost_packets_total`, `hertz_ingest_late_packets_total`
- `hertz_ingest_invalid_packets_total{reason}`
- `hertz_listeners{stream}`
- `hertz_sessions_active`, `hertz_sessions_total`, `hertz_session_errors_total{reason}`
- `hertz_session_join_seconds` histogram
- `hertz_listener_rtt_seconds`, `hertz_listener_packet_loss_ratio`, `hertz_listener_jitter_seconds`
- `hertz_listener_target_bitrate_bps`, `hertz_listener_outbound_bitrate_bps`
- `hertz_fanout_dropped_packets_total`, `hertz_fanout_written_packets_total`
- `hertz_source_target_bitrate_bps`
- Go runtime and process collectors

Listener/session IDs are deliberately **not** Prometheus labels; per-listener values are histograms. Debug a single session with `GET /v1/sessions/{id}`.

### Suggested alerts

- `hertz_ingest_last_packet_seconds` older than 5 s → source down.
- `rate(hertz_ingest_lost_packets_total[1m]) > 0` sustained → source network problem.
- `hertz_session_errors_total{reason="ip_limit"}` high → NAT/load-generator behavior or attack.
- p95 `hertz_listener_packet_loss_ratio` > 2% → path degradation.
- `hertz_fanout_dropped_packets_total` increasing → listener queues too small or CPU starvation.
- p99 join > 1 s → ICE/DTLS or control-plane saturation.

---

## 8. Performance bottlenecks

1. **Egress bandwidth** is the hard limit. 192 kbps × 1,000 listeners = 192 Mbps; × 5,000 = 960 Mbps. Add NIC capacity before CPU.
2. **Per-listener SRTP encryption and syscalls**. AES-NI/ARM crypto helps; one UDP mux reduces ports but not per-packet work.
3. **Per-subscriber packet copies** in `DownTrack.WriteRTP`. 50k copies/s at 1,000 listeners is fine; 250k/s at 5,000 is GC pressure. Optimize with pooled/refcounted buffers.
4. **GC**. `GOGC`/`GOMEMLIMIT` tuning; `GOMAXPROCS`; avoid per-packet allocations in the jitter buffer.
5. **Stream map contention**. `Broadcast` holds an RLock while enqueueing. Snapshot/copy-on-write the listener slice if contention appears.
6. **Pacer mode**. `HERTZ_PACER=leaky` queues without bound when target bitrate falls below source bitrate, converting congestion into latency. The default `noop` avoids that queue; source ABR and per-listener renditions are the real quality fixes.
7. **Join storms**. DTLS handshakes and certificate operations are CPU-heavy. Join rate limiting and staggered reconnects matter.
8. **File descriptors / memory per PeerConnection**. Two different measurements:
   - **Unconnected peer objects** (`BenchmarkPeerGetStats`, pprof): **32.7 KB/peer** at 500 and 1,000. `pc.GetStats()` costs **7.3–7.8 µs/peer**, allocating **2.49 MB per 1,000-peer sweep**; at the 5 s interval that is ~0.14% of one core and ~0.5 MB/s.
   - **Live sessions** on one host (loopback, 128 kbps source, join rate/burst raised deliberately for the ramp, 2026-11-07):

     | Sessions | Goroutines | `heap_alloc` | RSS | pprof in-use | NACK copy buffers |
     |---:|---:|---:|---:|---:|---:|
     | 500 | 12,041 | 882 MB | 1.10 GB | 510 MB | 385 MB |
     | 1,000 | 24,078 | 1.44 GB | 1.84 GB at sample; 3.06 GB after NACK fill | 664 MB | 448 MB |

     Pion's NACK responder defaults to a 1024-packet history (~20 s at 50 pps; verified in interceptor v0.1.51), which dominates the live allocation. Loss was ~3.3% at 500 and ~32% at 1,000 because client and server shared one 8-thread host; these are **not** capacity passes. Budget live memory from these measurements, not from the 32.7 KB unconnected figure. Tuning the NACK cache size is a follow-up item; no config flag exists yet.
9. **Windows** is fine for development, but Unixgram ingest and `SO_REUSEPORT`-style tuning are Linux-first.

### Experimental shared payload

`HERTZ_SHARED_PAYLOAD=true` makes DownTracks share the immutable Opus payload while still copying/rewriting the RTP header. It is **off by default**. Measured on 2026-11-07 (i7-3770, Go 1.27.3):

| Path | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `ClonePacket` | 463–588 | 1,208 | 4 |
| `ClonePacketSharedPayload` | 168–214 | 184 | 3 |

Over 500,000 clones: copy 576 MB allocated / 170 GCs / 8 ms total pause; shared 87 MB / 25 GCs / 4 ms. A full two-PeerConnection TWCC/NACK/SRTP test passes under `go test -race` with `HERTZ_RACE_SHARED_PAYLOAD=1`. Live multi-listener memory and audio behavior were not measured, so keep the default off until a soak test confirms it.

---

## 9. Load testing

The included `cmd/loadgen` creates real Pion PeerConnections, performs the HTTP offer/answer, receives RTP, and computes join success, time-to-first-packet, loss, duplicates, reordering, and bitrate.

The full test matrix, browser page, acoustic latency procedure, loadgen flag/output reference, pprof/CPU method, fault-injection cases, and the 1,500-listener benchmark gate are in **[docs/TESTING.md](docs/TESTING.md)**.

### Stable source

Use a file and loop it; a live `lavfi` source can drift or end:

```bash
ffmpeg -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=60" \
  -ac 2 -c:a libopus -b:a 128k test.opus

ffmpeg -hide_banner -re -stream_loop -1 -i test.opus -c copy \
  -payload_type 111 -f rtp "rtp://127.0.0.1:5004?pkt_size=1200"
```

### Stages

| Stage | Listeners | Ramp | Duration | Pass criteria |
|---|---:|---:|---:|---|
| Smoke | 1 | 1/s | 30 s | first packet < 1 s, 0 loss |
| Ramp | 100 | 20/s | 5 min | join ≥ 99%, loss < 0.1% |
| Target | 1,000 | 50/s | 10 min | join ≥ 99%, p95 first packet < 1 s, CPU < 70% |
| Stress | 2,000 | 50/s | 10 min | identify knee; no OOM/fd exhaustion |
| Soak | 500 | 10/s | 24 h | flat heap/goroutines, no leak |

Set `HERTZ_MAX_SESSIONS_PER_IP=0` and a high `HERTZ_MAX_SESSIONS` for single-host load tests, or run the load generator from many source IPs. Also raise `HERTZ_JOIN_RATE_PER_SECOND`, `HERTZ_JOIN_BURST`, `HERTZ_IP_JOIN_RATE_PER_SECOND`, and `HERTZ_IP_JOIN_BURST` deliberately for the ramp, and record those values. Otherwise the per-IP/global limiters will (correctly) return 429.

Example:

```bash
./bin/loadgen -url http://127.0.0.1:8080 -stream main \
  -listeners 1000 -ramp 50 -duration 10m -join-timeout 20s
```

Run the load generator on separate machines from the SFU so its CPU and NIC do not distort results. For 1,000+ listeners, run several processes with disjoint `-sub-prefix` values.

### Fault injection

On Linux, use `tc netem` on the listener-facing interface or a test namespace:

```bash
tc qdisc add dev eth0 root netem loss 1% delay 50ms 10ms reorder 1%
# run load test, watch hertz_listener_* metrics
tc qdisc del dev eth0 root
```

Test:
- 1%, 3%, 5% loss → NACK/Opus FEC/PLC behavior, audio continuity.
- 50/100/200 ms RTT → pacer and browser jitter buffer.
- Reordering 1–2% → ingest jitter buffer.
- Source kill/restart (new SSRC after 2 s silence) → buffer reset and recovery.
- Listener network change → `POST /renegotiate` with `iceRestart: true`.
- Token expiry/tamper, video SDP, oversized SDP, join flood → correct 4xx/429, no panic.

### Profiling

```bash
HERTZ_PPROF=true ./bin/hertz
go tool pprof http://127.0.0.1:9090/debug/pprof/profile?seconds=30
go tool pprof http://127.0.0.1:9090/debug/pprof/heap
```

Restrict the metrics listener to a private interface in production.

---

## 10. Security model

- **Media encryption:** DTLS-SRTP is mandatory; SRTP replay protection window is configured. TLS for signaling should be terminated by a reverse proxy.
- **Subscriber auth:** HMAC-SHA256 bearer token, constant-time compare, expiry with 30 s skew, kind/stream audience. Secret must be ≥ 16 bytes. Dev mode (`dev` token) is loudly warned and must never be enabled publicly.
- **Publisher auth:** raw RTP cannot carry a cryptographic identity. Use a private network/VPN, source CIDR allowlist, SSRC + UDP IP:port pinning, and Unix socket permissions. A source change is accepted only after 2 s of silence; otherwise it is treated as sequence poisoning. For authenticated remote publishers, add WHIP (DTLS-SRTP publisher) as a separate ingest path.
- **DoS:** global/per-IP session caps, join token bucket, audio-only SDP validation, SDP/body size caps, RTP packet size and PPS caps, ICE UDP mux, HTTP timeouts, pprof disabled by default.
- **Resource isolation:** per-listener bounded queues; slow listeners drop packets instead of blocking the broadcaster.
- **TURN:** deploy coturn for restrictive NATs. HERTZ can issue coturn TURN REST credentials (`HERTZ_TURN_REST_SECRET` + `HERTZ_TURN_URLS`); static TURN secrets are for the server ICE agent only and are rejected in `HERTZ_BROWSER_ICE_SERVERS`.

---

## 11. Scaling out

A single node is intentionally simple. For more than one node:

1. **Origin → edge:** one origin receives the mixer RTP; edge SFUs subscribe to the origin over WebRTC/QUIC and fan out to listeners. Each edge has its own ICE mux and public IP.
2. **Listener assignment:** consistent hashing by session or stream; a load balancer routes `/v1/subscribe` to an edge with capacity. Sticky sessions are required for renegotiation and DELETE unless session state is shared.
3. **Shared control state:** Redis/NATS for session ownership, capacity, and token revocation (future).
4. **Metrics aggregation:** Prometheus federation or remote write; keep listener IDs out of labels.
5. **Anycast / DNS:** route listeners to the nearest edge; keep origin ingest on a private network.

Bandwidth planning: `listeners × 192 kbps × 1.15` (SRTP/RTCP overhead). A 1 Gbps NIC is roughly 4,500 listeners at 192 kbps; leave headroom.

---

## 12. Known limitations / assumptions

- One logical stream per ingest socket; the `Hub` map supports multiple streams but dispatch/config is single-stream in v1.
- Opus passthrough only. No transcoding, no per-listener bitrate tiers. Source must be 48 kHz stereo Opus, ideally 20 ms ptime.
- Raw RTP publisher is network-authenticated, not cryptographically authenticated.
- No built-in TURN server; use coturn.
- Non-trickle SDP; join waits for ICE gathering.
- No recording, billing, content management, or web UI.
- Windows supports UDP ingest; Unixgram ingest is Linux-first.
- A source restart with a new SSRC/sequence/timestamp space is adopted only after 2 s of silence. Each `DownTrack` rewrites sequence numbers and timestamps continuously across that restart; Pion rewrites SSRC per listener. A restart inside the 2 s guard is rejected as an injection attempt.
- Pion's GCC/TWCC behavior should be benchmarked against your browser mix; `HERTZ_PACER=leaky` is the main latency risk when the path is below source bitrate.

---

## 13. Next steps

1. **WHIP ingest** for authenticated remote publishers (DTLS-SRTP + bearer token), alongside raw RTP.
2. **Renditions/transcoding ladder** (64 kbps mono, 128/192 kbps stereo) and per-listener track switching for real ABR.
3. **Origin/edge cascading** with QUIC or WebRTC relay, capacity-aware listener routing.
4. **TURN integration** with ephemeral credentials and coturn health checks.
5. **JWT/JWKS + one-time `jti`** for revocation and standards-based issuance.
6. **End-to-end latency instrumentation** (RTP header extension timestamp or audio watermark) and SLO dashboards.
7. **Fuzz tests** for RTP/SDP parsers; chaos tests for source restarts and network changes.
8. **NACK cache sizing**: live tests showed the Pion NACK responder's default packet cache dominates memory (~385 MB at 500 sessions, ~448 MB at 1,000). Add a tested `HERTZ_NACK_SIZE` knob rather than changing the default blindly.
9. **Benchmark gate**: 1,500 listeners/node; if missed, evaluate a Mediasoup/C++ data plane behind the existing Go control plane.

---

## 14. License

HERTZ is distributed under the GNU General Public License v3. See [LICENSE](LICENSE).

The `butt-master/` directory is a fork of **butt — Broadcast Using This Tool** and remains under the GNU General Public License v2. See [butt-master/COPYING](butt-master/COPYING). Treat it as a separate component when redistributing binaries or source.

---

## 14. Verification performed

**Environment:** Windows, Intel Core i7-3770, Go 1.27.3, Pion v4.2.26, MinGW-w64 GCC 16.2.0. All runs below were on 2026-11-07.

- `go build ./...` — pass.
- `go vet ./...` — pass.
- `go test ./...` — pass.
- `go test -race ./...` — pass with `CGO_ENABLED=1 CC=gcc` and portable MinGW-w64 16.2.0. A separate race run with `HERTZ_RACE_SHARED_PAYLOAD=1` exercised two live Pion PeerConnections with TWCC/NACK/SRTP; it passed.
- Unit tests cover jitter reordering, startup reorder, loss/late/duplicate, timestamp wrap, source pinning, source restart, per-listener seq/ts rewrite, token signing/expiry/tamper, ABR hysteresis, TURN REST credentials, XFF hop selection, SDP validation, admission limits, UDP ingest, and PeerStats millisecond JSON.
- E2E run A (single listener, 12:31 local, final code): FFmpeg looped `test.opus` (48 kHz stereo Opus, PT 111, 20 ms) → HERTZ → one headless Pion listener for 8 s. Result: 400 RTP packets, ~50 pps, 154,400 bps, 0 loss, first RTP 52 ms, join success 1/1.
- E2E run B (three listeners, 11:57 local): 3/3 joined, 1,489 packets over 12 s, ~150 pps aggregate, ~154 kbps per listener, 0 loss. First RTP p50 **58 ms**, p95 **239 ms**. Join requests took 31.8 ms total across three (mean 10.6 ms); p95 join was not computed for this run.
- 10-listener attempt (11:46 local): 8 joined, 2 returned 429 because the default `HERTZ_MAX_SESSIONS_PER_IP=8` was in effect. The source ended mid-run, so that run is not used for latency conclusions.
- `pc.GetStats()` benchmark (500 unconnected peers): 3.65 ms per full sweep, 7.31 µs/peer, 1.24 MB allocated. (1,000 unconnected peers): 7.30 ms per sweep, 7.30 µs/peer, 2.49 MB allocated. pprof heap delta: **32.7 KB/peer** at both 500 and 1,000; peers were created but not ICE-connected, so this is a lower bound.
- Live 500-listener run (12:26 local, same host, join limiters raised): 500/500 joined, 12,041 goroutines, `heap_alloc` 882 MB, RSS 1.10 GB, pprof in-use 510 MB; NACK copy buffers 385 MB. Loss ~3.3% because client and server shared the host; this is not a capacity pass.
- Live 1,000-listener run (12:27 local, same host, join limiters raised): 1,000/1,000 joined, 24,078 goroutines, `heap_alloc` 1.44 GB, RSS 1.84 GB at sample and 3.06 GB after NACK buffers filled, pprof in-use 664 MB; NACK copy buffers 448 MB. Loss ~32%; process CPU measured ~2.7 cores over 10 s (~33% of 8 GOMAXPROCS). This is not a capacity pass; it shows where the local host saturates.
- Packet clone benchmark (5 runs): copy **463–588 ns/op, 1,208 B/op, 4 allocs/op**; shared payload **168–214 ns/op, 184 B/op, 3 allocs/op**. GC comparison over 500,000 clones: copy 576 MB allocated / 170 GCs / 8 ms total pause; shared 87 MB / 25 GCs / 4 ms total pause. Shared payload remains **off by default** (`HERTZ_SHARED_PAYLOAD=false`).

- `cmd/hertz-admin` unit tests cover config normalization/validation, LOCALAPPDATA preference, port-availability probing (Windows exclusive bind), token generation/verification, secret redaction, Host/Origin checks, and metrics parsing. `go test ./cmd/hertz-admin` — pass.
- Admin first-run E2E (2026-11-07): `dist\HERTZ.exe` was started headless with a clean data directory; `POST /api/setup` extracted and started the embedded engine on test ports (HTTP 18080, ingest 15004, ICE 18443). The generated listener link was used by `loadgen`; a 5 s Opus sender produced 250 RTP packets, the listener received all 250 with 0 loss, and `/api/status` reported the source online.
- Admin operations: `POST /api/engine` stop/start worked; changing HTTP to 18081 restarted the engine on the new port; the admin-port validation rejected `8090`; rotating the auth secret made the old listener token return 401 and invalidated it as intended.
- Admin single-instance and lifecycle: a second `HERTZ.exe` detected the running admin and exited in 0.04 s in the headless run; in the tray-mode run on the same day a second double-click exited with code 0 in 0.58 s while the original admin and engine stayed alive; killing the admin process also killed the job-object-owned engine, leaving no orphan process.
- Admin UI routes `/`, `/setup`, `/settings`, `/listen`, `/assets/*` returned 200. On 2026-11-07 the in-app Chromium rendered `/setup`, `/`, and `/settings` without console errors; saving `/settings` restarted the embedded engine and metrics stayed bound to `127.0.0.1:19096` because `metrics_public` was off.
- First-run double-click check (2026-11-07 14:14 local, no `HERTZ_*` environment variables, clean `%LOCALAPPDATA%\HERTZ`): the process opened a visible console with the first-run banner and Chrome opened `http://127.0.0.1:8090/setup`. After `POST /api/setup`, the console window became hidden and a `SystrayClass` window appeared. A notification-area screenshot showed the HERTZ icon, and the tray menu was opened and visually verified: status/listener count, open dashboard, copy listener link, settings, stop/restart, open log folder, and quit.
- Embedded-browser limitation: the in-app Chromium rendered `/listen` and created a subscription, but its WebRTC session detached after about 20 s without receiving media (both from `127.0.0.1` and from the LAN address). The external Chrome listener stayed attached and the engine reported a live source and listener. Listener media flow therefore remains evidenced by the earlier external-Chrome/butt run (390 RTP packets over 8 s, 0 loss), not by the embedded browser.

**Not tested in this round:** audible Chrome/Safari/Firefox playback, acoustic end-to-end latency, Unix socket runtime on Linux, coturn/TURN REST against a real TURN server, end-to-end source restart/ICE restart, and the 1,500-listener benchmark gate. The tray menu was opened and its labels were verified, but the stop/restart/quit items were not clicked in this round. The live 500/1,000-session runs used loopback on the same host; they are memory observations, not capacity passes.

Build metadata can be injected with:

```bash
go build -ldflags "-X hertz/internal/version.Version=1.0.0 -X hertz/internal/version.Commit=$(git rev-parse --short HEAD)" ./cmd/hertz
```
