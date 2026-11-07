# HERTZ 0.2.0

**Release date:** 2026-11-07  
**License:** GPL-3.0 (HERTZ) · GPL-2.0 (butt-master fork)

HERTZ 0.2.0 adds a VLC-friendly Ogg/Opus HTTP stream, public metadata and now-playing APIs, a Windows installer, and several latency/caching fixes. Existing `%LOCALAPPDATA%\HERTZ` settings are preserved.

---

## Highlights

- **Direct Ogg/Opus stream** at `/live.ogg` for VLC and custom HTTP players
- **Public metadata JSON** at `/v1/public/status` with listener counts and now playing
- **Now playing editor** in the admin dashboard plus an authenticated publisher API
- **Windows installer** built with Inno Setup (per-user by default)
- **Admin UI in English** with listener/QR/iframe embed and direct audio/metadata links
- **Lower Ogg/HTTP latency** with an 8-packet server-side queue and proxy-buffering hints
- **Cache-safe admin assets** so an upgrade does not leave an old UI in the browser

---

## What’s new in 0.2.0

### Direct audio for VLC and custom players

- New endpoint: `GET /live.ogg`
- Ogg/Opus over HTTP, using the same Opus packets as WebRTC — no decode/re-encode
- VLC: **Media → Open Network Stream** → paste `http://<host>:8080/live.ogg`
- Chrome/Firefox can play it in an `<audio>` element; Safari support varies
- Slow HTTP clients drop packets instead of blocking WebRTC fanout
- HTTP listener cap: `HERTZ_MAX_HTTP_LISTENERS` (default 200)
- New `X-Accel-Buffering: no` response header for nginx reverse proxies
- Server-side queue capped at 8 packets (about 160 ms at 50 packets/s), down from 64 packets

### Public metadata and now playing

- New public endpoint: `GET /v1/public/status`
- Returns:
  - stream status and source bitrate
  - WebRTC, HTTP, and total listener counts
  - now-playing title/artist/updated time
  - relative `/live.ogg`, `/listen`, and metadata links
- New authenticated endpoint: `POST /v1/nowplaying`
  - requires a short-lived `KindPublish` token
  - accepts `{"title":"...","artist":"..."}`
  - limits each field to 200 characters and rejects control characters
  - send empty title and artist to clear
- Admin **Now playing** card signs a publisher token locally and forwards it; the token never reaches the browser

### Admin UI

- English UI throughout
- New **Direct audio & metadata** card with copy buttons
- New **Now playing** editor
- Existing listener link, QR, iframe embed, engine controls, and log viewer retained
- Assets use versioned URLs and `Cache-Control: no-store` to prevent stale UI after upgrades
- Fixed `fmtAge` showing “never” for a source that had just delivered audio; it now shows “just now”
- iframe embed mode at `/listen?embed=1` renders a compact 120 px player

### Windows installer

- New `HERTZ-Setup-0.2.0.exe` built with Inno Setup
- Per-user install by default (no administrator required)
- Installs under `%LOCALAPPDATA%\Programs\HERTZ`
- Start Menu shortcut; optional Desktop shortcut and “Start HERTZ when I sign in”
- All-users install available when launched as administrator
- All-users mode adds Windows Firewall rules for UDP 5004, UDP 8443, and TCP 8080
- Uninstaller preserves `%LOCALAPPDATA%\HERTZ` settings and logs
- Installer is unsigned; Windows SmartScreen may warn until a code-signing certificate is used

### Operations and security

- Admin UI remains loopback-only and rejects non-loopback peers, foreign `Host` headers, and cross-origin POSTs
- Admin POSTs require `X-HERTZ-Admin: 1`
- Metrics bind to `127.0.0.1` by default
- Public metadata exposes listener counts but never tokens, IPs, or credentials
- Documentation updated for VLC, metadata, now playing, installer, firewall, and latency tuning

---

## Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/listen` | Listener token in fragment | WebRTC listener page |
| GET | `/live.ogg` | None | Ogg/Opus stream for VLC/custom players |
| GET | `/v1/public/status` | None | Public metadata JSON |
| POST | `/v1/nowplaying` | Publisher token | Update title/artist |
| GET | `/v1/config` | Subscriber token | ICE servers and stream config |
| POST | `/v1/subscribe` | Subscriber token | Create a WebRTC session |
| GET | `/v1/sessions/{id}` | Subscriber token | Session stats |
| POST | `/v1/sessions/{id}/renegotiate` | Subscriber token | Renegotiation / ICE restart |
| DELETE | `/v1/sessions/{id}` | Subscriber token | Close a session |

Public metadata example:

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

Update now playing:

```bash
PUB_TOKEN=$(./bin/token -kind pub -secret "$HERTZ_AUTH_SECRET" -stream main -ttl 1h)

curl -s -X POST http://127.0.0.1:8080/v1/nowplaying \
  -H "Authorization: Bearer $PUB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Song name","artist":"Artist"}'
```

---

## Latency notes

Ogg/HTTP is inherently higher latency than WebRTC:

- TCP retransmission can block later packets (head-of-line blocking)
- VLC/player network cache adds buffering
- Browser `<audio>` adds its own buffer

HERTZ reduces its own contribution by:

- capping the Ogg sink queue at 8 packets (about 160 ms worst case at 50 packets/s)
- flushing each Ogg page
- sending `Cache-Control: no-store`
- sending `X-Accel-Buffering: no`

For lower VLC latency:

```powershell
vlc --network-caching=100 http://<host>:8080/live.ogg
```

If `/live.ogg` is behind nginx, disable buffering for that location:

```nginx
location /live.ogg {
    proxy_pass http://127.0.0.1:8080;
    proxy_buffering off;
    proxy_cache off;
}
```

For the lowest latency, use WebRTC via `/listen` or a WebRTC-capable custom player.

---

## Upgrade notes

- Existing `%LOCALAPPDATA%\HERTZ\config.json` is preserved.
- The engine now serves `/live.ogg`, `/v1/public/status`, and `/v1/nowplaying`; update both the admin app and any externally managed engine.
- TCP `8080` is used by the existing listener/signaling path; no new TCP port is needed for `/live.ogg` or metadata.
- UDP `5004` (RTP ingest) and UDP `8443` (ICE) must still be reachable.
- Per-user installs may prompt for Windows Firewall access; allow Private networks for the ports above.
- No config format changes.

---

## Validation

Checked with:

```text
go build ./...
go vet ./...
go test ./...
CGO_ENABLED=1 CC=gcc go test -race ./... -count=1
```

Additional checks:

- `ffprobe` recognized `/live.ogg` as `opus`, `48000 Hz`, 2 channels
- FFmpeg decoded the HTTP stream to a valid WAV
- VLC connected and produced a playable WAV from `/live.ogg`
- A 35-second HTTP stream received 705,363 bytes without a server write timeout
- Public metadata reported `http=1` while VLC was connected and `http=0` after disconnect
- Installer silent per-user install: exit 0, `FileVersion=0.2.0`
- Installer silent uninstall: exit 0, install directory removed

Not tested in this release:

- All-users installer mode with UAC elevation
- Safari/mobile Ogg/Opus playback
- End-to-end acoustic latency measurement
- 1,500-listener benchmark gate
- TURN/coturn in a real NAT environment

---

## Known limitations

- One logical stream per ingest socket in v1.
- Opus passthrough only; no transcoding or per-listener bitrate tiers.
- Raw RTP ingest is network-authenticated, not cryptographically authenticated.
- No built-in TURN server; use coturn.
- No recording, billing, or content management.
- HTTP audio has higher latency than WebRTC by design.
- VLC default network caching can delay first audio; lower it for faster startup.
- Now-playing metadata is in engine memory and is cleared when the engine restarts.
- Admin UI is loopback-only; do not expose `127.0.0.1:8090` through nginx/IIS.
- Installer is unsigned.

---

## Artifacts

| File | Bytes | SHA-256 |
|---|---:|---|
| `dist\HERTZ.exe` | 24,090,112 | `138AB50BAC8C8DB30097AD3176D40F7BBFEE5AB81618768511660B5F13195259` |
| `dist\HERTZ-Setup-0.2.0.exe` | 7,491,881 | `823B718E8B30A3967BE7BA85E78F776EC56942DB8AA80C8167B35263CD258BE3` |
| `dist\HERTZ-windows-x64.zip` | 9,747,873 | `BC63B65C241BDDE788F9A94E4B1AC13AD3BB10BB133DFAAD0D4B5864DBDD17DF` |

Build locally:

```powershell
.\scripts\build-admin.ps1
.\scripts\build-installer.ps1
```

---

## License

HERTZ is distributed under the GNU General Public License v3. See [LICENSE](../LICENSE).

The `butt-master/` directory is a fork of **butt — Broadcast Using This Tool** and remains under the GNU General Public License v2. See [butt-master/COPYING](../butt-master/COPYING). Treat it as a separate component when redistributing binaries or source.
