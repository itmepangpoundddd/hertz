# Hertz-butt — Broadcast Using This Tool

Hertz-butt is the HERTZ fork of **butt** (broadcast using this tool), with a redesigned dark FLTK interface and a `HERTZ RTP` server type. It supports ShoutCast and IceCast and runs on Linux, MacOS and Windows. It streams live audio from a microphone or line input to an Icecast/Shoutcast server or to HERTZ as raw Opus RTP. Recording is also possible.

> **HERTZ RTP adaptation:** the `HERTZ RTP` server type sends raw Opus RTP/UDP to HERTZ. See [HERTZ.md](HERTZ.md) and the portable `dist/hertz-butt-windows-x64.zip`.

## This Repository

This repository has been created out of the need to have butt read audio input from other channels than the first two of an audio device.

## Compiling

Theoretically it should compile the same way as before; install dependencies, hit `./configure && make` and you should be all set.

However, for me those instructions didn’t work on macOS Sierra. Here is how I did make it work (together with a couple of fixes that are in the repository):

```
$ CPPFLAGS="-ObjC++ -I../xcode/include" ./configure
$ make
```

I have no idea how to fix the underlying issues because frankly, I despise C or C++ or whatever unholy mongrel of a language this tool is written in.

## Closing Remarks

Anyway, this works for me. It doesn’t seem to crash, the choices I make are persisted, and my world has gotten a little bit better because of this.

Big thanks to Daniel Nöthen for creating butt in the first place; without him I’d have nothing to patch.
