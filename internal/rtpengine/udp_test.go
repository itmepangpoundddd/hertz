package rtpengine

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestUDPIngestReadsWithSingleBuffer(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		MaxPPS:        1000,
		JitterWindow:  5 * time.Millisecond,
	}, log)
	u, err := NewUDPIngest("127.0.0.1:0", h, nil, 0, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = u.Run(ctx) }()
	defer u.Close()

	conn, err := net.Dial("udp", u.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(rawPacket(t, 1, 111, 0x1111)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.Snapshot().Packets == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("packet not accepted: %+v", h.Snapshot())
}
