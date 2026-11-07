# HERTZ Tutorial — เริ่มใช้ตั้งแต่ศูนย์จนฟังเสียงได้

> **เป้าหมายของ tutorial นี้:** ทำให้ HERTZ รับเสียง Opus จาก FFmpeg ผ่าน RTP/UDP แล้วส่งต่อให้ browser หรือ headless listener ผ่าน WebRTC ได้ภายในเครื่องเดียว โดยใช้เวลาประมาณ 10–15 นาที

> **สถานะของคำสั่ง:** tutorial นี้อิงโค้ดปัจจุบัน (Go 1.26+, Pion v4.2.26) และ local flow ถูกทดสอบบน Windows ด้วย FFmpeg + Pion loadgen แล้ว ส่วน browser page, TURN/coturn, Unix socket และ benchmark 1,500 listeners ยังไม่ได้ทดสอบจริงในรอบนี้ — ดู `docs/TESTING.md`

---

## 0. HERTZ คืออะไรใน 60 วินาที

HERTZ เป็น **SFU (Selective Forwarding Unit)** สำหรับเสียงอย่างเดียว:

```text
FFmpeg / mixer
  │ Opus RTP/UDP :5004
  ▼
HERTZ ingest
  │ ตรวจ RTP, jitter buffer 40 ms, จัดลำดับ packet
  ▼
HERTZ SFU
  │ แยก queue ต่อ listener, เข้ารหัส SRTP, DTLS, ICE
  ▼
Browser / loadgen
  │ decode Opus, jitter buffer, NACK/FEC/PLC
  ▼
เสียงออกลำโพง
```

HERTZ **ไม่ decode/re-encode Opus** จึงเหมาะกับเสียง 48 kHz stereo 128–256 kbps และพยายามเก็บ latency ไว้ต่ำกว่า 500 ms

---

## 1. สิ่งที่ต้องติดตั้ง

| เครื่องมือ | ใช้ทำอะไร | ตรวจสอบ |
|---|---|---|
| Go 1.26+ | build HERTZ | `go version` |
| FFmpeg | สร้างและส่ง Opus RTP | `ffmpeg -version` |
| Browser (Chrome/Safari/Firefox) | ฟังเสียง | เปิด `web/test.html` |
| Python 3 (ทางเลือก) | เสิร์ฟไฟล์ HTML | `python --version` |
| curl | ตรวจ HTTP/metrics | `curl --version` |

> Windows: ถ้าไม่มี Go ให้ติดตั้งจาก go.dev หรือใช้ portable ZIP แล้วเพิ่ม `go/bin` เข้า `PATH`
>
> FFmpeg ต้องมี encoder `libopus`; ตรวจด้วย `ffmpeg -encoders | findstr opus` (Windows) หรือ `ffmpeg -encoders | grep opus`

---

## 2. Build HERTZ

เปิด PowerShell หรือ terminal ที่โฟลเดอร์โปรเจกต์:

```powershell
go build -o bin/hertz.exe ./cmd/hertz
go build -o bin/loadgen.exe ./cmd/loadgen
go build -o bin/token.exe ./cmd/token
```

Linux/macOS:

```bash
go build -o bin/hertz ./cmd/hertz
go build -o bin/loadgen ./cmd/loadgen
go build -o bin/token ./cmd/token
```

ถ้าต้องการฝัง version:

```powershell
go build -ldflags "-X hertz/internal/version.Version=0.1.0 -X hertz/internal/version.Commit=demo" `
  -o bin/hertz.exe ./cmd/hertz
```

---

## 2.5 ทางลัดสำหรับ Windows: `HERTZ.exe` (ไม่ต้องใช้ terminal)

ถ้าไม่ต้องการตั้ง environment variable เอง ให้ build แอป admin ไฟล์เดียว:

```powershell
.\scripts\build-admin.ps1
# ได้ dist\HERTZ.exe
```

ขั้นตอนใช้งาน:

1. ดับเบิลคลิก `dist\HERTZ.exe`
2. หน้าต่าง cmd จะเปิดและเบราว์เซอร์จะไปที่ `http://127.0.0.1:8090/setup`
3. ตั้งชื่อสตรีม, IP ที่ผู้ฟังใช้, พอร์ต, และ CIDR ต้นทาง
4. กด **บันทึกและเปิดระบบ** แล้วรอ 10–15 วินาที
5. ไปที่แดชบอร์ด → **ลิงก์ผู้ฟัง** → คัดลอกหรือให้สแกน QR
6. ตั้งค่า butt เป็น `HERTZ RTP`, Address = IP ของเครื่องนี้, Port = `5004`, Codec = Opus 48 kHz stereo

ครั้งต่อไปที่เปิด `HERTZ.exe` โปรแกรมจะอยู่ใน **system tray** และเริ่ม engine ให้อัตโนมัติ คลิกขวาที่ไอคอนเพื่อเปิดแดชบอร์ด/หยุด/ปิดโปรแกรม

ไฟล์ที่โปรแกรมสร้าง:

```text
%LOCALAPPDATA%\HERTZ\config.json
%LOCALAPPDATA%\HERTZ\engine\hertz-engine.exe
%LOCALAPPDATA%\HERTZ\logs\hertz.log
```

รายละเอียด firewall, TURN, การแชร์ผ่านอินเทอร์เน็ต และการแก้ปัญหา อยู่ใน **[ADMIN.md](ADMIN.md)**

> ส่วนที่เหลือของ tutorial นี้เป็นโหมด manual สำหรับพัฒนา/ทดสอบ หรือใช้บน Linux server

---

## 3. โหมด demo บนเครื่องเดียว (10 นาที)

โหมดนี้ใช้ `HERTZ_ALLOW_INSECURE_DEV=true` เพื่อให้ token `dev` ใช้ได้ทันที **ห้ามใช้ใน production**

### 3.1 Terminal 1 — เริ่ม HERTZ

PowerShell:

```powershell
$env:HERTZ_ALLOW_INSECURE_DEV = "true"
$env:HERTZ_AUTH_SECRET = "0123456789abcdef0123456789abcdef"
$env:HERTZ_HTTP_ADDR = ":8080"
$env:HERTZ_METRICS_ADDR = ":9090"
$env:HERTZ_INGEST_ADDR = ":5004"
$env:HERTZ_ICE_UDP_PORT = "8443"
$env:HERTZ_ICE_LOOPBACK = "true"
$env:HERTZ_STREAM_ID = "main"
$env:HERTZ_LOG_LEVEL = "info"

.\bin\hertz.exe
```

Linux/macOS:

```bash
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

ถ้าสำเร็จจะเห็นประมาณ:

```text
RTP ingest listening addr=[::]:5004
ICE UDP mux listening port=8443
signaling listening addr=:8080
metrics listening addr=:9090
```

ถ้า Windows Firewall ถาม ให้ Allow เฉพาะ **Private networks** และไม่ติ๊ก Public

### 3.2 Terminal 2 — สร้างไฟล์เสียงทดสอบ

สร้าง Opus 48 kHz stereo 60 วินาที:

```powershell
ffmpeg -hide_banner -loglevel warning -y `
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=60" `
  -ac 2 -c:a libopus -b:a 128k test.opus
```

จากนั้นส่งแบบ loop ไปที่ HERTZ:

```powershell
ffmpeg -hide_banner -loglevel warning -re -stream_loop -1 -i test.opus `
  -c copy -payload_type 111 -f rtp "rtp://127.0.0.1:5004?pkt_size=1200"
```

Linux/macOS:

```bash
ffmpeg -hide_banner -loglevel warning -re -stream_loop -1 -i test.opus \
  -c copy -payload_type 111 -f rtp "rtp://127.0.0.1:5004?pkt_size=1200"
```

FFmpeg จะพิมพ์ SDP ออกมา; HERTZ ใช้ raw RTP จึงไม่ต้องใช้ SDP นี้ สิ่งสำคัญคือ `opus/48000/2`, PT 111 และ 20 ms frames

### 3.3 ตรวจว่า ingest ทำงาน

เปิดอีก terminal:

```powershell
(Invoke-WebRequest -UseBasicParsing http://127.0.0.1:9090/metrics).Content |
  Select-String "hertz_ingest_packets_total|hertz_ingest_bitrate_bps|hertz_ingest_jitter_seconds"
```

Linux/macOS:

```bash
curl -s http://127.0.0.1:9090/metrics |
  grep -E 'hertz_ingest_packets_total|hertz_ingest_bitrate_bps|hertz_ingest_jitter_seconds'
```

ต้องเห็น `hertz_ingest_packets_total` เพิ่มขึ้นเรื่อย ๆ และ bitrate ประมาณ 128–160 kbps

ถ้า packet เป็น 0:
- ตรวจว่า FFmpeg ยังรันอยู่
- ตรวจ PT 111 ให้ตรงกับ `HERTZ_INGEST_PT`
- ตรวจ firewall/port 5004/udp
- ตรวจ log HERTZ ว่ามี `invalid` reason อะไร

### 3.4 สร้าง token สำหรับ browser

ใน terminal ใหม่:

```powershell
.\bin\token.exe -secret "0123456789abcdef0123456789abcdef" `
  -sub demo-user -stream main -ttl 1h
```

จะได้ token หน้าตาประมาณ `eyJ...` เก็บไว้ใช้ใน browser

> โหมด demo ใช้ token `dev` แทนได้ แต่ควรลอง flow token จริงเพื่อให้เหมือน production

### 3.5 เปิด browser test page

เสิร์ฟโฟลเดอร์ `web`:

```powershell
python -m http.server 8000 --directory web
```

ถ้าไม่มี Python ใช้ static server อื่น หรือเปิด `web/test.html` โดยตรง (แต่บาง browser จะบล็อก fetch จาก `file://`)

เปิด:

```text
http://127.0.0.1:8000/test.html
```

กรอก:
- API base: `http://127.0.0.1:8080`
- Stream ID: `main`
- Token: token จากข้อ 3.4

กด **Connect** แล้วรอ:
- state เปลี่ยนเป็น `subscribed: <session-id>`
- มีเสียง 440 Hz ออกลำโพง
- stats แสดง `state`, `target_bps`, `rtt_ms`, `jitter_ms`, `loss_ratio`, `outbound_bps`

ถ้า browser ไม่เล่นเสียง ให้กด play ที่ audio element หรือตรวจ autoplay policy

### 3.6 ทางเลือก: ทดสอบด้วย loadgen แทน browser

```powershell
.\bin\loadgen.exe `
  -url http://127.0.0.1:8080 `
  -stream main `
  -token dev `
  -listeners 1 `
  -ramp 1 `
  -duration 15s
```

ผลที่ควรมองหา:

```text
joined 1, failed 0
pps ~50
rx_bps ~154000
loss_ratio 0
SUMMARY ... first_packet_p50_ms ~50
```

`pps ~50` คือ Opus 20 ms (50 packets/วินาที) ไม่ใช่ 50 kbps

---

## 4. ดู session และ metrics

### 4.1 ดู session เดียว

เอา `session_id` จาก browser page หรือ log ของ HERTZ:

```powershell
$id = "ใส่-session-id"
$token = "ใส่-token"
Invoke-WebRequest -UseBasicParsing `
  -Uri "http://127.0.0.1:8080/v1/sessions/$id" `
  -Headers @{ Authorization = "Bearer $token" } |
  Select-Object -ExpandProperty Content
```

ผลจะมี:

```json
{
  "state": "connected",
  "target_bps": 256000,
  "rtt_ms": 1.2,
  "jitter_ms": 0.4,
  "loss_ratio": 0,
  "outbound_bps": 152856,
  "down_track": {
    "Written": 200,
    "Dropped": 0,
    "QueueLen": 0,
    "QueueCap": 10
  }
}
```

### 4.2 Metrics ที่ควรดู

```text
hertz_ingest_packets_total
hertz_ingest_bitrate_bps
hertz_ingest_jitter_seconds
hertz_ingest_lost_packets_total
hertz_ingest_late_packets_total
hertz_ingest_invalid_packets_total{reason}
hertz_listeners
hertz_sessions_active
hertz_listener_rtt_seconds
hertz_listener_packet_loss_ratio
hertz_listener_outbound_bitrate_bps
hertz_fanout_dropped_packets_total
```

### 4.3 Prometheus + Docker (ทางเลือก)

```bash
docker compose up --build
```

- HERTZ: `http://127.0.0.1:8080`
- Prometheus UI: `http://127.0.0.1:9091`
- metrics: `http://127.0.0.1:9090/metrics`

compose ใช้ `HERTZ_PUBLIC_IP=127.0.0.1` และ dev auth สำหรับ demo เท่านั้น

---

## 5. Troubleshooting

| อาการ | สาเหตุที่พบบ่อย | วิธีแก้ |
|---|---|---|
| `hertz_ingest_packets_total` เป็น 0 | FFmpeg ไม่ได้ส่ง, PT ไม่ตรง, firewall | ตรวจ `-payload_type 111`, port 5004/udp, log invalid reason |
| ได้ 401 | token ผิด/หมดอายุ/secret ไม่ตรง | ออก token ใหม่ด้วย secret เดียวกับ HERTZ |
| ได้ 403 | token ผูก stream อื่น | ใช้ token ที่ `-stream main` หรือ token แบบ `*` |
| ได้ 429 | per-IP/global session หรือ join rate | demo: ตั้ง `HERTZ_MAX_SESSIONS_PER_IP=0`; load test: เพิ่ม `HERTZ_JOIN_RATE_PER_SECOND`, `HERTZ_IP_JOIN_RATE_PER_SECOND` |
| ICE failed | ไม่มี candidate ที่ browser ไปถึง | local ใช้ `HERTZ_ICE_LOOPBACK=true`; production ตั้ง `HERTZ_PUBLIC_IP` และเปิด UDP 8443 |
| ต่อได้แต่ไม่มีเสียง | autoplay/browser audio, source หยุด | กด play, ตรวจ `hertz_listener_outbound_bitrate_bps`, ตรวจ FFmpeg |
| เสียงกระตุก | loss/jitter สูง | ดู `hertz_listener_packet_loss_ratio`, `jitter_ms`; ลองเพิ่ม jitter window หรือใช้ TURN |
| Windows Firewall เด้ง | มี socket ใหม่ | Allow Private only; loopback ใช้ได้แม้ block |
| port 8080/8443/5004 ถูกใช้ | process เก่าค้าง | `Get-Process hertz,ffmpeg` แล้ว stop หรือเปลี่ยน port |

### ตรวจ token แบบเร็ว

```powershell
.\bin\token.exe -secret "0123456789abcdef0123456789abcdef" -sub test -stream main -ttl 5m
```

```bash
curl -i http://127.0.0.1:8080/v1/config \
  -H "Authorization: Bearer <token>"
```

ต้องได้ 200 และมี `ice_servers`, `stream_id`, `codec`

---

## 6. โหมด production-ish

### 6.1 ตั้งค่าความปลอดภัยขั้นต่ำ

```powershell
$env:HERTZ_ALLOW_INSECURE_DEV = "false"
$env:HERTZ_AUTH_SECRET = "<random-32-bytes>"
$env:HERTZ_ICE_LOOPBACK = "false"
$env:HERTZ_PUBLIC_IP = "<public-ip-of-sfu>"
$env:HERTZ_INGEST_ALLOWED_CIDRS = "10.0.0.0/8,192.168.0.0/16"
$env:HERTZ_INGEST_SSRC = "<optional-pinned-ssrc>"
$env:HERTZ_MAX_SESSIONS_PER_IP = "8"
$env:HERTZ_PPROF = "false"
```

 checklist:
- เปิด UDP `8443` และ `5004` เฉพาะ source ที่จำเป็น
- เปิด TCP `8080` ผ่าน reverse proxy/TLS
- ไม่เปิด `9090` สู่ public
- ใช้ WireGuard/private VLAN สำหรับ raw RTP ingest
- ตั้ง `HERTZ_INGEST_ALLOWED_CIDRS` และ pin SSRC
- หมุน secret และออก token อายุสั้น
- ใช้ TURN/coturn สำหรับ listener หลัง NAT เข้ม

> Raw RTP ไม่มี cryptographic publisher authentication. ถ้าต้องการ auth ผู้ประกาศแบบครบ ให้ใช้ WireGuard/mTLS sidecar หรือเพิ่ม WHIP ในอนาคต

### 6.2 TURN REST

HERTZ แยก ICE server ฝั่ง server (`HERTZ_ICE_SERVERS`) กับ browser (`HERTZ_BROWSER_ICE_SERVERS`) และรองรับ credential ชั่วคราว:

```powershell
$env:HERTZ_TURN_REST_SECRET = "<coturn-static-auth-secret>"
$env:HERTZ_TURN_URLS = "turn:turn.example.net:3478?transport=udp"
$env:HERTZ_TURN_TTL = "1h"
```

`/v1/config` จะคืน username/password ชั่วคราวให้ browser โดยไม่ส่ง static secret ออกไป

ดูตัวอย่าง coturn ใน `docs/TESTING.md` §6

---

## 7. Load test และ fault injection

### 7.1 Load test พื้นฐาน

```powershell
.\bin\loadgen.exe `
  -url http://127.0.0.1:8080 `
  -stream main `
  -token dev `
  -listeners 100 `
  -ramp 20 `
  -duration 60s
```

อ่านผล:
- `joined` / `attempted` → join success
- `pps` → packet rate รวม
- `rx_bps` → bitrate รวม
- `loss_ratio` → loss จาก sequence gaps
- `duplicates`, `reordered`
- `SUMMARY first_packet_p95_ms`

### 7.2 Load test ใหญ่

ถ้า client กับ server อยู่เครื่องเดียวกัน ผลจะบิดเบือนจาก CPU/NIC ที่แชร์กัน ควรแยกเครื่อง และตั้งค่า server:

```powershell
$env:HERTZ_MAX_SESSIONS_PER_IP = "0"
$env:HERTZ_MAX_SESSIONS = "5000"
$env:HERTZ_JOIN_RATE_PER_SECOND = "1000"
$env:HERTZ_JOIN_BURST = "1000"
$env:HERTZ_IP_JOIN_RATE_PER_SECOND = "1000"
$env:HERTZ_IP_JOIN_BURST = "1000"
```

แล้วรัน loadgen จากเครื่องแยก หรือหลาย process:

```powershell
.\bin\loadgen.exe ... -listeners 500 -ramp 50 -duration 10m -sub-prefix gate-a
.\bin\loadgen.exe ... -listeners 500 -ramp 50 -duration 10m -sub-prefix gate-b
.\bin\loadgen.exe ... -listeners 500 -ramp 50 -duration 10m -sub-prefix gate-c
```

> อย่าใช้ค่าที่เพิ่ม rate นี้ใน production โดยไม่ประเมินผล

### 7.3 Fault injection

บน Linux/WSL2/VM:

```bash
# loss 1%
sudo tc qdisc add dev eth0 root netem loss 1%
# รัน load test
sudo tc qdisc del dev eth0 root

# RTT 100 ms
sudo tc qdisc add dev eth0 root netem delay 100ms 10ms
sudo tc qdisc del dev eth0 root

# reorder
sudo tc qdisc add dev eth0 root netem reorder 1% 25%
sudo tc qdisc del dev eth0 root
```

ทดสอบ source restart, ICE restart, token ปลอม/หมดอายุ, video SDP, oversized SDP และ join flood ตาม `docs/TESTING.md` §7

---

## 8. รัน unit test และ race

```powershell
gofmt -w ./cmd ./internal
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

`go test -race` ต้องมี cgo/gcc ถ้า Windows ไม่มี:

```powershell
# ใช้ portable MinGW-w64
$env:PATH = "C:\tools\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
go test -race ./... -count=1
```

หรือใช้ WSL2/Docker ตาม `docs/TESTING.md` §1

---

## 9. หยุดทุกอย่าง

ในแต่ละ terminal กด `Ctrl+C` หรือ:

```powershell
Get-Process hertz,loadgen,ffmpeg -ErrorAction SilentlyContinue | Stop-Process
```

ลบไฟล์ทดสอบ:

```powershell
Remove-Item test.opus -ErrorAction SilentlyContinue
```

---

## 10. ข้อจำกัดที่ควรรู้ก่อนใช้จริง

- HERTZ v1 เป็น **Opus passthrough** ไม่มี transcoding/rendition ต่อ listener
- ไม่มี per-listener bitrate tier; ใช้ GCC + source ABR + Opus FEC/PLC
- NACK cache ของ Pion default 1,024 packets ทำให้ memory ต่อ session สูง (วัดได้ ~0.5–1 MB/session ใน live test)
- Live 500/1,000 sessions บนเครื่องเดียวมี loss สูง; ยังไม่ได้ผ่าน benchmark gate 1,500 listeners บนเครื่องแยก
- Raw RTP publisher auth เป็น network-level ไม่ใช่ cryptographic
- Source restart ภายใน 2 วินาทีจะถูก reject; หลัง 2 วินาทีต้องเปลี่ยน SSRC ด้วย
- Browser page/TURN/Unix socket ยังไม่ได้ทดสอบจริงในรอบนี้

---

## 11. ไปต่อ

- วัด acoustic latency จริงด้วย click/tone ตาม `docs/TESTING.md` §3
- รัน 1,500-listener gate จากเครื่องแยก
- ทดสอบ Chrome/Safari/Firefox
- พิจารณาเพิ่ม `HERTZ_NACK_SIZE` หลังวัด memory/loss
- พิจารณาเปิด `HERTZ_SHARED_PAYLOAD` หลัง soak test
- เพิ่ม WHIP ingest, transcoded renditions, origin/edge cascading ตาม README §13
