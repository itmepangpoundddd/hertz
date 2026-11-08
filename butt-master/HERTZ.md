# Hertz-butt with HERTZ RTP

This directory is a small adaptation of **butt** for temporary live broadcasts to HERTZ. The app is branded **Hertz-butt** and uses a redesigned FLTK interface.

The normal butt output is Ogg/Opus over Icecast/Shoutcast. This build adds a third server type:

```text
HERTZ RTP
```

It captures the same microphone/line input through PortAudio, encodes Opus at 48 kHz, and sends **raw Opus RTP/UDP** to HERTZ instead of an HTTP stream.

## Quick start

1. Start HERTZ with UDP ingest (default `:5004`):

   ```powershell
   $env:HERTZ_ALLOW_INSECURE_DEV="true"
   $env:HERTZ_AUTH_SECRET="0123456789abcdef0123456789abcdef"
   $env:HERTZ_INGEST_ADDR=":5004"
   $env:HERTZ_ICE_LOOPBACK="true"
   .\bin\hertz.exe
   ```

2. Build/copy `Hertz-butt.exe` next to `butt-hertz.bat`.
3. Double-click `butt-hertz.bat`.
4. Press the connect/play button in Hertz-butt.
5. Open the HERTZ listener page/link and press play.

To open settings directly:

```powershell
.\Hertz-butt.exe -c hertz.buttrc --settings
```

The main window uses a modern dark layout with status cards, a smooth stereo
level meter, and a compact activity log. Settings uses the same palette.

The included `hertz.buttrc` is a local demo preset:
- server type: HERTZ RTP
- address: `127.0.0.1`
- port: `5004`
- codec: Opus
- bitrate: 128 kbps
- samplerate: 48000 Hz
- stereo
- connect at startup: enabled

`butt-hertz.bat` copies the preset to `%TEMP%\hertz-butt.buttrc` so your normal butt configuration is not modified.

Tested on 2026-11-07 with the portable UCRT64 build:
- Hertz-butt connected to HERTZ as **HERTZ RTP**, Opus 48 kHz stereo, 128 kbps.
- HERTZ received ~133 kbps / 50 pps with zero invalid packets.
- A headless HERTZ listener received 390 packets over 8 s with 0 loss and ~133 kbps.
- First RTP arrived at the listener after 278 ms on loopback (join included; not acoustic latency).

## Manual configuration

In Hertz-butt: **Settings → Add server**
- Type: **HERTZ RTP**
- Address: HERTZ host/IP
- Port: HERTZ `HERTZ_INGEST_ADDR` port (default `5004`)
- Password/mount/user: not used for HERTZ
- Audio codec: **Opus**
- Samplerate: 48000 Hz
- Channels: stereo
- Bitrate: 128–256 kbps

HERTZ only accepts Opus for this path. If another codec is selected, Hertz-butt refuses to connect.

## Build

The repository is an autotools/FLTK project. On Windows with MSYS2/UCRT64:

```bash
pacman -S --needed mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-fltk1.3 \
  mingw-w64-ucrt-x86_64-portaudio mingw-w64-ucrt-x86_64-opus \
  mingw-w64-ucrt-x86_64-libogg mingw-w64-ucrt-x86_64-libvorbis \
  mingw-w64-ucrt-x86_64-lame mingw-w64-ucrt-x86_64-flac \
  mingw-w64-ucrt-x86_64-fdk-aac mingw-w64-ucrt-x86_64-libsamplerate \
  autoconf automake make pkgconf

# from butt-master/
autoreconf -fi
./configure
make -j4
```

The resulting `src/butt.exe` needs the UCRT64 runtime DLLs. Copy it to `Hertz-butt.exe` and place the required `ucrt64/bin/*.dll` files next to it for a portable folder, or run it from the MSYS2 UCRT64 shell.

## Important limitations

- HERTZ RTP has no cryptographic publisher authentication. Use it on a private LAN, WireGuard, or a firewall that allows only the butt host to UDP `5004`.
- There is no metadata/song update over HERTZ RTP.
- UDP is connectionless: butt cannot detect that HERTZ stopped receiving unless the local send fails. Check HERTZ `hertz_ingest_packets_total`.
- This is a temporary/event tool, not a hardened production encoder.
