// HERTZ RTP output for butt
//
// Sends raw Opus RTP/UDP to a HERTZ ingest socket. HERTZ expects:
//   Opus 48 kHz, 20 ms frames, payload type 111, one Opus frame per RTP packet.
//
// This is intentionally a direct RTP path. No Icecast/Ogg container is used.

#ifndef HERTZ_H
#define HERTZ_H

#include "opus_encode.h"

// Returns 0 on success, non-zero on failure (same convention as ic_connect).
int hz_connect(void);

// Encodes one 20 ms frame from pcm_buf and sends one RTP packet.
// Returns 0 on success, -1 on socket/encoder failure.
int hz_send_frame(opus_enc *opus, short *pcm_buf, int samples);

void hz_disconnect(void);
int hz_is_connected(void);

#endif
