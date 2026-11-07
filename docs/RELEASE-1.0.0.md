# HERTZ 1.0.0

**Release date:** 2026-11-07  
**License:** GPL-3.0 (HERTZ) · GPL-2.0 (butt-master fork)

HERTZ 1.0.0 is the first feature-complete release of the audio-only WebRTC SFU for live radio. It ingests raw Opus RTP from butt, a mixer, or FFmpeg, reorders packets once, and fans the same Opus stream out to WebRTC listeners, HTTP audio players, and custom metadata clients.

---

## Highlights

- **Audio-only WebRTC SFU** built with Go and Pion v4
- **Raw Opus RTP ingest** over UDP or Unix datagram
- **One-click Windows app** with first-run setup, system tray, and an embedded engine
- **English admin UI** with listener links, QR codes, and an iframe embed snippet
- **Direct Ogg/Opus HTTP stream** for VLC and custom players
- **Public metadata JSON** with listener counts and now-playing data
- **Now playing editor** in the dashboard plus an authenticated publisher API
- **Inno Setup desktop installer** with per-user and all-users modes
- **Prometheus metrics**, structured logs, and engine lifecycle controls

---

## What’s new

### Core engine

- Opus 48 kHz stereo passthrough with 20 ms frames
- RTP/UDP ingest with source CIDR allowlist and payload-type validation
- Unix datagram ingest for Linux-first deployments
- Jitter buffer, reordering, duplicate/late packet handling
- NACK, FEC, and PLC behavior through Pion
- Source-side ABR controller with encoder control hook
- Per-listener down tracks with non-blocking fanout and drop counters
- Session limits, per-IP limits, join-rate limiting, and session TTL
- Embedded TURN credential support (static TURN servers are not exposed to browsers)
- Prometheus metrics for ingest, listeners, RTT, jitter, loss, and fanout

### Windows app and admin UI

- Single `HERTZ.exe` with the engine embedded as a child process
- First launch shows a console and opens `http://127.0.0.1:8090/setup`
- Later launches run hidden in the system tray
- Windows job object kills the engine when the admin process exits
- Start/stop/restart engine from the dashboard or tray
- Log viewer and “open log folder” action
- Listener link and QR generation with token kept in the URL fragment
- iframe embed card with copy and preview actions
- English UI throughout, with `?v=` asset URLs and `Cache-Control: no-store` to avoid stale UI after upgrades
- Config, engine, and logs stored under `%LOCALAPPDATA%\HERTZ`

### Listener and player options

- WebRTC listener page at `/listen` with a compact `?embed=1` mode
- Direct Ogg/Opus HTTP stream at `/live.ogg`
- VLC-compatible: Media → Open Network Stream → paste the `/live.ogg` URL
- HTTP listener count included in public metadata
- Slow HTTP clients drop packets instead of blocking WebRTC fanout
- `HERTZ_MAX_HTTP_LISTENERS` cap (default 200)

### Public metadata and now playing

- Public read-only endpoint: `GET /v1/public/status`
- Returns stream status, source bitrate, listener counts, now playing, and relative links
- `POST /v1/nowplaying` with a short-lived `KindPublish` token
- Admin dashboard **Now playing** card; the admin process signs and forwards the token locally
- Title and artist limited to 200 characters; control characters rejected
- Clear metadata by sending empty title and artist

### Operations, security, and packaging

- Admin UI binds to loopback and rejects non-loopback peers, foreign `Host` headers, and cross-origin POSTs
- Admin POST requests require `X-HERTZ-Admin: 1`
- Metrics bind to `127.0.0.1` by default; optional public binding
- Host/Origin checks on the admin listener
- Windows firewall rules for UDP 5004, UDP 8443, and TCP 8080 when installed for all users
- Inno Setup installer with per-user default, Start Menu/Desktop shortcuts, and optional start-at-login
- Uninstaller preserves `%LOCALAPPDATA%\HERTZ` settings and logs
- butt integration: `HERTZ RTP` server type, preset, launcher, and portable ZIP
- Documentation for setup, testing, administration, TURN, and troubleshooting

---

## Public endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/listen` | Listener token in fragment | WebRTC listener page |
| GET | `/live.ogg` | None | Ogg/Opus HTTP stream for VLC/custom players |
| GET | `/v1/public/status` | None | Public metadata JSON |
| POST | `/v1/nowplaying` | Publisher token | Update title/artist |
| GET | `/v1/config` | Subscriber token | ICE servers and stream config |
| POST | `/v1/subscribe` | Subscriber token | Create a WebRTC listener session |
| GET | `/v1/sessions/{id}` | Subscriber token | Session stats |
| POST | `/v1/sessions/{id}/renegotiate` | Subscriber token | ICE restart / renegotiation |
| DELETE | `/v1/sessions/{id}` | Subscriber token | Close a session |

Example public metadata:

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

Update now playing from a playout script:

```bash
PUB_TOKEN=$(./bin/token -kind pub -secret "$HERTZ_AUTH_SECRET" -stream main -ttl 1h)

curl -s -X POST http://127.0.0.1:8080/v1/nowplaying \
  -H "Authorization: Bearer $PUB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Song name","artist":"Artist"}'
```

---

## Quick start

1. Run `HERTZ-Setup-1.0.0.exe`, or launch the portable `HERTZ.exe`.
2. The first-run setup page opens at `http://127.0.0.1:8090/setup`.
3. Set the stream ID, public listener address, ports, and source CIDR.
4. Press **Save and start** and wait 10–15 seconds.
5. Configure butt: **Settings → Add server → HERTZ RTP**, address = HERTZ machine IP, port = `5004`, Opus 48 kHz stereo.
6. Share the listener link/QR, or use:
   - `http://<host>:8080/live.ogg` in VLC
   - `http://<host>:8080/v1/public/status` for custom players

---

## Upgrade notes

- Existing `%LOCALAPPDATA%\HERTZ\config.json` is preserved by the installer and uninstaller.
- The engine now serves `/live.ogg`, `/v1/public/status`, and `/v1/nowplaying`; update both the admin app and any externally managed engine.
- TCP `8080` is used by the existing listener/signaling path, so no new TCP port is required for `/live.ogg` or public metadata.
- UDP `5004` (RTP ingest) and UDP `8443` (ICE) must still be reachable from sources/listeners.
- If you installed per-user, Windows Firewall may prompt for UDP access; allow Private networks.
- Admin UI assets are no longer cached for an hour; a normal refresh picks up the new UI after an update.

---

## Validation

The release build was checked with:

```text
go build ./...
go vet ./...
go test ./...
CGO_ENABLED=1 CC=gcc go test -race ./... -count=1
```

Additional checks performed during development:

- `ffprobe` recognized `/live.ogg` as `opus`, `48000 Hz`, 2 channels.
- FFmpeg decoded a 3-second sample to a valid WAV.
- VLC opened `/live.ogg`, connected as an HTTP listener, and produced a playable WAV.
- A 35-second HTTP stream received 705,363 bytes without being cut by the server write timeout.
- Public metadata reported `http=1` while VLC was connected and `http=0` after disconnect.
- The Inno Setup installer completed a silent per-user install and uninstall with exit code 0.
- The live dashboard showed `On air`, listener links, QR, embed code, direct audio URL, and metadata URL.

Not tested in this release:

- All-users installer mode with UAC elevation
- Safari and mobile browser playback of Ogg/Opus
- 1,500-listener benchmark gate
- End-to-end acoustic latency measurement
- TURN/coturn in a real NAT environment

---

## Known limitations

- One logical stream per ingest socket in v1; the Hub supports multiple streams internally, but configuration is single-stream.
- Opus passthrough only; no transcoding or per-listener bitrate tiers.
- Raw RTP ingest is network-authenticated, not cryptographically authenticated.
- No built-in TURN server; use coturn and `HERTZ_TURN_*` settings.
- No recording, billing, content management, or built-in web CMS.
- HTTP audio is Ogg/Opus; Safari support varies. Keep `/listen` (WebRTC) for browser listeners.
- HTTP audio has inherently higher latency than WebRTC: TCP head-of-line blocking plus player buffering. HERTZ caps its own server-side queue at 8 packets (about 160 ms at 50 packets/s), but VLC's default network cache adds more. Use `--network-caching=100`/`200` and disable proxy buffering when low latency matters. This is not an end-to-end latency measurement.
- Now-playing metadata is stored in engine memory and is cleared when the engine restarts.
- Public metadata exposes listener counts but never tokens, IPs, or credentials.
- Admin UI is loopback-only; do not expose `127.0.0.1:8090` through IIS/nginx.
- The installer is unsigned; Windows SmartScreen may warn until a code-signing certificate is used.

---

## Artifacts

Build locally with:

```powershell
.\scripts\build-admin.ps1          # dist\HERTZ.exe + dist\HERTZ-windows-x64.zip
.\scripts\build-installer.ps1      # dist\HERTZ-Setup-<version>.exe
```

Artifacts attached to the GitHub release should be built from the same commit as this tag. The build scripts print the SHA-256 of every output.

---

## License

HERTZ is distributed under the GNU General Public License v3. See [LICENSE](../LICENSE).

The `butt-master/` directory is a fork of **butt — Broadcast Using This Tool** and remains under the GNU General Public License v2. See [butt-master/COPYING](../butt-master/COPYING). Treat it as a separate component when redistributing binaries or source.
