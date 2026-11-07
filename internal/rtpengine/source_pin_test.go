package rtpengine

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestHandlerPinsUDPSource(t *testing.T) {
	h := NewHandler(HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		MaxPPS:        1000,
		JitterWindow:  10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	now := time.Now()
	if !h.Handle(rawPacket(t, 1, 111, 0x1111), now, "127.0.0.1:1000") {
		t.Fatal("first source rejected")
	}
	if h.Handle(rawPacket(t, 2, 111, 0x1111), now.Add(time.Millisecond), "127.0.0.1:2000") {
		t.Fatal("different UDP IP:port accepted before silence")
	}
	if s := h.Snapshot(); s.BadSource != 1 {
		t.Fatalf("badSource=%d want 1", s.BadSource)
	}
	// Same SSRC from a new IP:port is still poisoning even after silence.
	if h.Handle(rawPacket(t, 2, 111, 0x1111), now.Add(3*time.Second), "127.0.0.1:2000") {
		t.Fatal("same-SSRC source change after silence accepted")
	}
	if !h.Handle(rawPacket(t, 5000, 111, 0x2222), now.Add(3*time.Second), "127.0.0.1:2000") {
		t.Fatal("restart with new SSRC after silence rejected")
	}
}
