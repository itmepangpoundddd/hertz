package sfu

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

// OpusCapability is the single audio codec HERTZ forwards. The SFU never
// re-encodes: the source must produce Opus at 48 kHz stereo. These fmtp values
// describe the stream to the browser and request in-band FEC and stereo.
func OpusCapability(maxAverageBitrate int) webrtc.RTPCodecCapability {
	if maxAverageBitrate <= 0 {
		maxAverageBitrate = 256_000
	}
	if maxAverageBitrate > 510_000 {
		maxAverageBitrate = 510_000
	}
	return webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  2,
		SDPFmtpLine: fmt.Sprintf(
			"minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1;maxaveragebitrate=%d",
			maxAverageBitrate,
		),
	}
}
