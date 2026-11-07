package sfu

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPeerStatsJSONUsesMilliseconds(t *testing.T) {
	b, err := json.Marshal(PeerStats{
		RTT:         1500 * time.Microsecond,
		Jitter:      2 * time.Millisecond,
		NoPacketFor: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["rtt_ms"] != 1.5 {
		t.Fatalf("rtt_ms=%v want 1.5", got["rtt_ms"])
	}
	if got["jitter_ms"] != float64(2) {
		t.Fatalf("jitter_ms=%v want 2", got["jitter_ms"])
	}
	if got["no_packet_for_ms"] != float64(3000) {
		t.Fatalf("no_packet_for_ms=%v want 3000", got["no_packet_for_ms"])
	}
}
