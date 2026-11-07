package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"hertz/internal/metrics"
	"hertz/internal/rtpengine"
)

func TestRunIngestMetricsWiresSnapshot(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := rtpengine.NewHandler(rtpengine.HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		MaxPPS:        1000,
		JitterWindow:  time.Millisecond,
	}, log)
	p := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			SequenceNumber: 1,
			Timestamp:      960,
			SSRC:           0x1111,
			PayloadType:    111,
		},
		Payload: []byte{0xf8, 0x01, 0x02},
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !h.Handle(raw, time.Now(), "127.0.0.1:1000") {
		t.Fatal("packet rejected")
	}
	m := metrics.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runIngestMetrics(ctx, m, h, "test")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if testutil.ToFloat64(m.IngestPackets.WithLabelValues("test")) >= 1 {
			if got := testutil.ToFloat64(m.IngestBytes.WithLabelValues("test")); got <= 0 {
				t.Fatalf("ingest bytes=%v want >0", got)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ingest metrics were not updated from PacketHandler.Snapshot")
}
