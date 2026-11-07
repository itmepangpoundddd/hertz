package sfu

import (
	"encoding/json"
	"time"
)

// MarshalJSON ensures duration fields with _ms names are actual milliseconds,
// not Go's default time.Duration nanoseconds. This is used by debug endpoints
// and any future serialization of PeerStats.
func (p PeerStats) MarshalJSON() ([]byte, error) {
	type wire struct {
		RTTMS         float64        `json:"rtt_ms"`
		JitterMS      float64        `json:"jitter_ms"`
		Loss          float64        `json:"loss_ratio"`
		TargetBitrate int            `json:"target_bitrate_bps"`
		OutboundBPS   int64          `json:"outbound_bps"`
		PacketsSent   uint64         `json:"packets_sent"`
		BytesSent     uint64         `json:"bytes_sent"`
		NoPacketMS    float64        `json:"no_packet_for_ms"`
		DownTrack     DownTrackStats `json:"down_track"`
		SampledAt     time.Time      `json:"sampled_at"`
	}
	return json.Marshal(wire{
		RTTMS:         float64(p.RTT) / float64(time.Millisecond),
		JitterMS:      float64(p.Jitter) / float64(time.Millisecond),
		Loss:          p.Loss,
		TargetBitrate: p.TargetBitrate,
		OutboundBPS:   p.OutboundBPS,
		PacketsSent:   p.PacketsSent,
		BytesSent:     p.BytesSent,
		NoPacketMS:    float64(p.NoPacketFor) / float64(time.Millisecond),
		DownTrack:     p.DownTrack,
		SampledAt:     p.SampledAt,
	})
}
