# HERTZ Admin — คู่มือผู้ดูแล (Windows)

> เป้าหมาย: ผู้ดูแลที่ไม่ใช่นักพัฒนาสามารถดาวน์โหลดไฟล์เดียว ดับเบิลคลิก ตั้งค่าครั้งแรกในเบราว์เซอร์ แล้วแชร์ลิงก์ให้ผู้ฟังได้ โดยไม่ต้องใช้ terminal หรือแก้ environment variable

---

## 1. ไฟล์ที่ต้องใช้

แนะนำ:

```text
dist\HERTZ-Setup-0.1.0.exe
```

หรือใช้แบบ portable:

```text
dist\HERTZ.exe
```

`HERTZ.exe` เป็นไฟล์เดียวที่รวม:

| ส่วน | รายละเอียด |
|---|---|
| Admin UI | หน้าเว็บตั้งค่า/แดชบอร์ด ที่ `http://127.0.0.1:8090` |
| HERTZ engine | ถูกฝังใน exe และแตกเป็นไฟล์ลูกตอนเริ่มทำงาน |
| Tray | ไอคอนข้างนาฬิกาสำหรับเปิด/หยุด/ปิดระบบ |
| Listener page | หน้าเว็บผู้ฟังที่ `/listen` พร้อม token ใน fragment |

ไม่ต้องติดตั้ง Go, DLL หรือ runtime เพิ่ม และ **ไม่ต้องใช้ IIS**

---

## 2. เริ่มใช้ครั้งแรก

1. ดับเบิลคลิก `HERTZ.exe`
2. จะมีหน้าต่าง cmd เปิดขึ้น แสดงที่อยู่ `http://127.0.0.1:8090`
3. เบราว์เซอร์จะเปิด **หน้าตั้งค่าครั้งแรก** ให้อัตโนมัติ
   - ถ้าไม่เปิด ให้คัดลอก `http://127.0.0.1:8090` ไปวางในเบราว์เซอร์
4. กรอกข้อมูลขั้นต่ำ:
   - **ชื่อสตรีม** — ใช้ `main` ได้
   - **ที่อยู่ที่ผู้ฟังใช้เปิดลิงก์** — ใส่ IP ในวง LAN ของเครื่องนี้ เช่น `192.168.1.10`
   - **พอร์ต** — ใช้ค่าเริ่มต้นได้: HTTP `8080`, metrics `9090`, ingest `5004`, ICE `8443`
   - **อนุญาต RTP จาก CIDR** — ค่าเริ่มต้นมี `127.0.0.1/32` และวง LAN ที่ตรวจพบ
5. กด **บันทึกและเปิดระบบ** แล้วรอ 10–15 วินาที
6. ระบบจะพาไปแดชบอร์ด และหน้าต่าง cmd จะซ่อนเข้ากระบวนการ system tray

หลังตั้งค่าเสร็จ ครั้งต่อไปที่ดับเบิลคลิก `HERTZ.exe` โปรแกรมจะไม่เปิด cmd ค้างไว้ แต่จะไปอยู่ใน **system tray** และเริ่ม engine ให้อัตโนมัติ

> ถ้าดับเบิลคลิกซ้ำขณะโปรแกรมทำงานอยู่ ตัวที่สองจะตรวจพบตัวแรกและเปิดแดชบอร์ดให้ทันที ไม่สร้าง engine ซ้ำ

### 2.1 ติดตั้งด้วย HERTZ-Setup

- ดับเบิลคลิก `HERTZ-Setup-0.1.0.exe`
- ค่าเริ่มต้นติดตั้งแบบ **per-user** ไม่ต้องใช้สิทธิ์ admin ลงที่ `%LOCALAPPDATA%\Programs\HERTZ`
- สร้าง Start Menu shortcut และเลือก Desktop shortcut / Start with Windows ได้
- ถ้ารันด้วยสิทธิ์ admin และเลือกติดตั้งสำหรับ all users จะเพิ่ม Windows Firewall rules ให้ UDP `5004`, UDP `8443` และ TCP `8080`
- ถ้าติดตั้งแบบ per-user แล้ว Windows Firewall ถาม ให้ allow บน **Private networks** สำหรับพอร์ตข้างต้น
- uninstall ไม่ลบ `%LOCALAPPDATA%\HERTZ` (config/log) เพื่อให้ตั้งค่าเดิมยังอยู่
- installer ยังไม่ได้ code-sign ดังนั้น Windows SmartScreen อาจเตือน
- **ไม่ต้องใช้ IIS** — HERTZ มี HTTP server ในตัวสำหรับ admin/listener/VLC/metadata

---

## 3. ระบบ tray

คลิกขวาที่ไอคอน HERTZ ข้างนาฬิกา:

| เมนู | การทำงาน |
|---|---|
| เปิดแผงควบคุม | เปิดแดชบอร์ดในเบราว์เซอร์ |
| คัดลอกลิงก์ผู้ฟัง | สร้าง token ใหม่และคัดลอกลิงก์ |
| ตั้งค่า | เปิดหน้าตั้งค่า |
| เริ่มระบบ / หยุดระบบ | เริ่มหรือหยุด engine |
| เริ่มระบบใหม่ | รีสตาร์ท engine |
| เปิดโฟลเดอร์ log | เปิด `%LOCALAPPDATA%\HERTZ\logs` |
| ออกจากโปรแกรม | หยุด engine และปิด tray |

ถ้าไม่เห็นไอคอน ให้กดลูกศร `^` ในถาดระบบ หรือตรวจใน Task Manager ว่ามี `HERTZ.exe` ทำงานอยู่

---

## 4. ไฟล์และโฟลเดอร์ที่โปรแกรมสร้าง

```text
%LOCALAPPDATA%\HERTZ\
  config.json                 การตั้งค่า (มี auth secret)
  engine\hertz-engine.exe     engine ที่แตกจาก exe
  logs\hertz.log              log ของ engine
```

- `config.json` เก็บ secret สำหรับเซ็น token — **ห้ามแชร์ไฟล์นี้**
- log ถูกหมุนเมื่อใหญ่เกิน 5 MB เป็น `hertz.log.1`
- ถ้าต้องการย้ายการตั้งค่า ให้ปิดโปรแกรมก่อนแล้วคัดลอกทั้งโฟลเดอร์ `HERTZ`

---

## 5. แชร์ลิงก์ให้ผู้ฟัง

ในแดชบอร์ด การ์ด **ลิงก์ผู้ฟัง**:

- เลือกอายุลิงก์ (1 ชั่วโมง ถึง 30 วัน)
- กด **สร้างลิงก์ใหม่**
- กด **คัดลอก** แล้วส่งลิงก์ให้ผู้ฟัง
- หรือให้ผู้ฟังสแกน **QR**

ลิงก์มีรูปแบบ:

```text
http://192.168.1.10:8080/listen#t=<token>
```

- token อยู่ใน fragment (`#t=...`) จึงไม่ถูกส่งไปใน HTTP request log หรือ Referer
- token หมดอายุตามที่เลือก — สร้างใหม่ได้ตลอด
- **สร้างรหัสลับใหม่** ในหน้าตั้งค่าจะทำให้ลิงก์เก่าทั้งหมดใช้ไม่ได้ทันที
- ผู้ฟังเปิดลิงก์แล้วกด **เริ่มฟัง** ไม่ต้องติดตั้งหรือกรอก token

### ฝัง player ไว้ในเว็บ

แดชบอร์ดมีการ์ด **Embed the player on your website** ซึ่งสร้าง iframe snippet จากลิงก์ผู้ฟังปัจจุบันให้อัตโนมัติ กด **Copy embed code** แล้ววางในหน้าเว็บของคุณ:

```html
<iframe
  src="http://192.168.1.10:8080/listen?embed=1#t=<token>"
  width="100%" height="140"
  style="border:0;border-radius:12px"
  allow="autoplay"
  title="HERTZ live audio"></iframe>
```

- `?embed=1` เปิดโหมด compact สำหรับ iframe (ซ่อนหัวข้อและคำอธิบาย)
- token ยังอยู่ใน fragment และไม่ถูกส่งไปใน HTTP request
- หน้า admin บล็อก iframe ของตัวเองด้วย CSP (`frame-ancestors 'none'`) จึงใช้ปุ่ม **Preview embedded player** เปิดในแท็บใหม่แทน
- ถ้าเว็บแม่เป็น HTTPS จะฝัง player HTTP ไม่ได้เพราะ mixed content — ต้องใช้ HTTPS/reverse proxy กับ HERTZ ด้วย

### ลิงก์เสียงสำหรับ VLC และ metadata สำหรับ custom player

แดชบอร์ดการ์ด **Direct audio & metadata** แสดงสองลิงก์ที่ไม่ต้องใช้ token:

```text
ลิงก์เสียง:   http://192.168.1.10:8080/live.ogg
ลิงก์ข้อมูล:  http://192.168.1.10:8080/v1/public/status
```

- `/live.ogg` เป็น Ogg/Opus HTTP stream — เปิดใน VLC ได้ผ่าน **Media → Open Network Stream**
- VLC ค่าเริ่มต้นอาจมี network cache หลายวินาทีก่อนเสียงเริ่ม ถ้าต้องการเริ่มเร็วขึ้นให้ลด `--network-caching` เช่น `--network-caching=200`
- HERTZ ใช้ Opus packet เดียวกับ WebRTC ไม่ decode/re-encode
- Chrome/Firefox เล่น Ogg/Opus ใน `<audio>` ได้ ส่วน Safari บางรุ่นรองรับจำกัด — สำหรับ browser ทั่วไปยังแนะนำ `/listen`
- `/v1/public/status` เป็น JSON แบบอ่านอย่างเดียว:

```json
{
  "stream_id": "radio",
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

- จำนวน HTTP listener จำกัดด้วย `HERTZ_MAX_HTTP_LISTENERS` (ค่าเริ่มต้น 200)
- ถ้าเว็บ player เป็น HTTPS จะโหลดลิงก์เสียง HTTP ไม่ได้เพราะ mixed content
- metadata เก็บในหน่วยความจำของ engine และจะหายเมื่อรีสตาร์ท engine

### อัปเดตชื่อเพลง (Now playing)

ในแดชบอร์ด การ์ด **Now playing** ให้กรอก Title/Artist แล้วกด **Save now playing** — admin จะสร้าง publisher token อายุสั้นและส่งต่อไป engine เอง โดย token ไม่หลุดไปที่ browser

ถ้าต้องการอัปเดตจาก playout software โดยตรง:

```bash
PUB_TOKEN=$(./bin/token -kind pub -secret "$HERTZ_AUTH_SECRET" -stream radio -ttl 1h)
curl -s -X POST http://127.0.0.1:8080/v1/nowplaying \
  -H "Authorization: Bearer $PUB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Song name","artist":"Artist"}'
```

Title/Artist ยาวได้ไม่เกิน 200 ตัวอักษร และห้ามมี control character; ส่งค่าว่างทั้งคู่เพื่อล้างข้อมูล

> ใครมีลิงก์นี้สามารถฟังได้จนกว่าจะหมดอายุ จึงควรส่งเฉพาะกลุ่มผู้ฟังที่ต้องการ

---

## 6. ตั้งค่าเครื่องส่งเสียง (butt)

ใช้ชุด `butt-master\dist\hertz-butt-windows-x64.zip` หรือ butt เวอร์ชันที่รองรับ HERTZ RTP:

```text
Type:       HERTZ RTP
Address:    IP ของเครื่องที่รัน HERTZ (หรือ 127.0.0.1 ถ้าเครื่องเดียวกัน)
Port:       พอร์ต RTP Ingest (ค่าเริ่มต้น 5004)
Codec:      Opus
Samplerate: 48000 Hz
Channels:   Stereo
Bitrate:    128 kbps หรือ 192 kbps
```

- Address ต้องเป็น IP/ชื่อเครื่องเท่านั้น ห้ามใส่ `http://` หรือ `:5004` ต่อท้าย
- HERTZ RTP เป็น UDP และไม่มีรหัสผ่าน publisher
- ตรวจว่าการ์ด **สถานะต้นทาง** ขึ้น “กำลังออกอากาศ”

---

## 7. Windows Firewall

ต้องเปิดพอร์ตเหล่านี้เมื่อต้นทางหรือผู้ฟังอยู่คนละเครื่อง (เปิด PowerShell แบบ Administrator):

```powershell
# ผู้ฟังเชื่อม signaling / เปิดหน้า listen
netsh advfirewall firewall add rule name="HERTZ listener HTTP" dir=in action=allow protocol=TCP localport=8080

# WebRTC media (ICE/SRTP)
netsh advfirewall firewall add rule name="HERTZ ICE UDP" dir=in action=allow protocol=UDP localport=8443

# RTP จากเครื่องส่งเสียง — จำกัดเฉพาะวง LAN ของคุณ
netsh advfirewall firewall add rule name="HERTZ ingest UDP" dir=in action=allow protocol=UDP localport=5004 remoteip=192.168.1.0/24
```

- ไม่ต้องเปิด inbound สำหรับ admin `8090`
- metrics `9090` bind เฉพาะ `127.0.0.1` โดยค่าเริ่มต้น ถ้าติ๊ก **ให้เครื่องอื่นอ่าน metrics ได้** ในตัวเลือกขั้นสูง จึงต้องเปิด firewall TCP ตามพอร์ตนั้น
- ถ้าเปลี่ยนพอร์ตในหน้าตั้งค่า ต้องแก้ firewall rule ตาม

---

## 8. ใช้งานผ่านอินเทอร์เน็ต

HERTZ Admin ตั้งใจให้ admin UI อยู่บน `127.0.0.1` เท่านั้น สำหรับผู้ฟังนอกวง LAN:

1. ตั้ง **ที่อยู่ที่ผู้ฟังใช้เปิดลิงก์** เป็นโดเมน/IP สาธารณะ
2. Port-forward TCP `8080` (signaling/listen) และ UDP `8443` (ICE) ที่ router
3. ถ้าเครื่องอยู่หลัง NAT แบบ 1:1 ให้ใส่ **Public IP** ในหน้าตั้งค่า
4. แนะนำให้วาง reverse proxy ที่มี TLS หน้า TCP `8080` เพื่อให้ผู้ฟังใช้ `https://`
5. ถ้าผู้ฟังอยู่หลัง NAT ที่เชื่อมตรงไม่ได้ ให้ตั้ง **TURN** (coturn) ในหน้าตั้งค่า
6. **ห้าม** port-forward `8090` (admin) หรือ `9090` (metrics)

> เวอร์ชันนี้ยังไม่มี auto-start ตอนเปิดเครื่อง, Windows Service, code signing หรือตัวอัปเดตอัตโนมัติ

---

## 9. แก้ปัญหา

### เปิดเว็บตั้งค่าไม่ได้

- ตรวจว่ามี `HERTZ.exe` ทำงานใน Task Manager
- เปิด `http://127.0.0.1:8090/healthz` ควรเห็น JSON `{"app":"hertz-admin"}`
- ถ้าพอร์ต 8090 ถูกโปรแกรมอื่นใช้ ให้ปิดโปรแกรมนั้นแล้วเปิด HERTZ ใหม่

### ขึ้น “พอร์ต ... ถูกใช้งานอยู่”

```powershell
netstat -ano | findstr :8080
netstat -ano | findstr :5004
```

ปิดโปรแกรมที่ใช้พอร์ตนั้น หรือเปลี่ยนพอร์ตในหน้าตั้งค่า

### Engine ไม่ยอมเริ่ม

- เปิดแดชบอร์ด → **Log ล่าสุด**
- หรือดู `%LOCALAPPDATA%\HERTZ\logs\hertz.log`
- สาเหตุที่พบบ่อย: พอร์ตชน, firewall, หรือ engine ตัวเก่าค้าง (ปิดจาก Task Manager แล้วเปิดใหม่)

### ต้นทางขึ้น “รอสัญญาณต้นทาง”

- ตรวจว่า butt เชื่อมต่อแล้วและแสดง `Connection established`
- ตรวจ firewall UDP ingest
- ตรวจ CIDR ต้นทางในหน้าตั้งค่าให้รวม IP ของเครื่อง butt
- ดู `hertz_ingest_packets_total` ที่ `http://127.0.0.1:9090/metrics`

### ผู้ฟังเปิดลิงก์แล้วไม่มีเสียง

- กดปุ่ม **เริ่มฟัง** อีกครั้ง (เบราว์เซอร์อาจบล็อก autoplay)
- ตรวจว่าลิงก์ไม่หมดอายุ
- ตรวจ firewall UDP `8443`
- ถ้าผู้ฟังอยู่นอก LAN ต้องมี public IP/port-forward/TURN ที่ถูกต้อง
- ลองเปิด `/listen` บนเครื่อง HERTZ เองก่อน เพื่อแยกปัญหาเครื่องผู้ฟัง

### ไอคอน tray หาย

- Windows 11 อาจซ่อนไอคอนใหม่ในถาดซ่อน — กด `^`
- ตรวจ `HERTZ.exe` ใน Task Manager
- ถ้าโปรแกรมถูกปิด ให้เปิด `HERTZ.exe` ใหม่

---

## 10. ถอนการติดตั้ง

1. คลิกขวาไอคอน HERTZ → **ออกจากโปรแกรม**
2. ลบ `HERTZ.exe`
3. ลบ `%LOCALAPPDATA%\HERTZ`
4. ลบ firewall rule ที่สร้างไว้ (ถ้าต้องการ):

```powershell
netsh advfirewall firewall delete rule name="HERTZ listener HTTP"
netsh advfirewall firewall delete rule name="HERTZ ICE UDP"
netsh advfirewall firewall delete rule name="HERTZ ingest UDP"
```

---

## 11. ความปลอดภัย

- Admin UI bind เฉพาะ `127.0.0.1` และตรวจ `Host`/`Origin` เพื่อกัน DNS rebinding
- ทุกคำสั่ง POST ต้องมี header `X-HERTZ-Admin: 1` จากหน้า UI เดียวกัน
- listener token เป็น HMAC-SHA256 อายุจำกัดและอยู่ใน URL fragment
- `config.json` เก็บ secret — จำกัดสิทธิ์การเข้าถึงโฟลเดอร์
- TURN secret ใช้สร้าง credential ชั่วคราว ไม่ถูกส่งไปที่เบราว์เซอร์
- metrics bind เฉพาะ loopback เว้นแต่ผู้ดูแลเปิดใช้เองในตัวเลือกขั้นสูง
- อ่านเพิ่มเติม: [`docs/TUTORIAL.md`](TUTORIAL.md), [`docs/TESTING.md`](TESTING.md)

---

## 12. Build เอง

จากโฟลเดอร์โปรเจกต์:

```powershell
.\scripts\build-admin.ps1
```

ถ้า PowerShell บล็อกสคริปต์ (`running scripts is disabled`):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-admin.ps1
```

สคริปต์จะ:

1. build engine เป็น `cmd\hertz-admin\assets\hertz-engine.exe`
2. compile icon/version resource (`rsrc_windows_amd64.syso`) ถ้ามี `windres`
3. build `dist\HERTZ.exe`
4. แสดง SHA-256 ของไฟล์

ต้องมี Go 1.26+ และไม่ต้องใช้ CGO สำหรับ `HERTZ.exe`

สร้าง installer ต่อด้วย Inno Setup 6/7:

```powershell
.\scripts\build-installer.ps1
# -> dist\HERTZ-Setup-0.1.0.exe
```

### รุ่นที่ build ล่าสุด (2026-11-07)

| ไฟล์ | ขนาด | SHA-256 |
|---|---:|---|
| `dist\HERTZ.exe` | 24,089,088 ไบต์ | `8189D8574D47781CB3FE3FDF400126D52AD3623EAAFC927B2710A53F0B589C37` |
| `dist\HERTZ-Setup-0.1.0.exe` | 7,490,082 ไบต์ | `B30731ADBB77C9A446DBC7FBD6D3EA8F37477FAB5F10081619C489A022F63055` |
| `dist\HERTZ-windows-x64.zip` | 9,745,791 ไบต์ | `31374A7322576E35D468283F531C33464A3E0615B3E0D85F1ACAF425EE195495` |

> installer ยังไม่ได้ code-sign; SmartScreen อาจเตือนจนกว่าจะมี code-signing certificate

ตรวจไฟล์ก่อนแจกจ่ายได้ด้วย:

```powershell
Get-FileHash .\HERTZ.exe -Algorithm SHA256
```

ผลตรวจรุ่นนี้: ดับเบิลคลิกครั้งแรก (ไม่มีตัวแปร `HERTZ_*`, ไม่มี `%LOCALAPPDATA%\HERTZ`) เปิด console และ Chrome ไปที่หน้า `/setup`; หลังบันทึกตั้งค่า console ซ่อนและมีไอคอน HERTZ ในถาดระบบ เมนู tray เปิดได้และแสดงสถานะ ผู้ฟัง ลิงก์ผู้ฟัง ตั้งค่า หยุด/เริ่มระบบ เปิดโฟลเดอร์ log และออกจากโปรแกรม ดับเบิลคลิกซ้ำขณะโปรแกรมทำงานอยู่จะเปิดแดชบอร์ดเดิมแล้วจบโปรเซสที่สองภายใน 0.58 วินาที

ข้อจำกัดที่พบ: เบราว์เซอร์ในตัวของแอป (embedded Chromium) เปิดหน้า `/listen` และสร้าง session ได้ แต่ session จะหลุดหลังประมาณ 20 วินาทีโดยไม่ได้รับเสียง ทั้งที่เข้าผ่าน `127.0.0.1` และ LAN address ส่วน Chrome ภายนอกยังคงเชื่อมต่ออยู่ (session ไม่หลุด) และค่า `hertz_fanout_written_packets_total` เพิ่มขึ้น การทดสอบเสียงผู้ฟังจึงอ้างอิงการรันด้วย Chrome ภายนอก/butt (390 แพ็กเก็ตใน 8 วินาที loss 0) ไม่ใช่ embedded browser
