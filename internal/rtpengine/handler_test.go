package rtpengine

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func rawPacket(t *testing.T, seq uint16, pt uint8, ssrc uint32) []byte {
	t.Helper()
	p := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			SequenceNumber: seq,
			Timestamp:      uint32(seq) * 960,
			SSRC:           ssrc,
			PayloadType:    pt,
		},
		Payload: []byte{0xf8, 0xff, 0xfe},
	}
	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandlerValidatesPayloadTypeAndSSRC(t *testing.T) {
	h := NewHandler(HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		MaxPPS:        1000,
		JitterWindow:  10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	now := time.Now()
	src := "127.0.0.1:1000"
	if !h.Handle(rawPacket(t, 1, 111, 0x1111), now, src) {
		t.Fatal("valid packet rejected")
	}
	if h.Handle(rawPacket(t, 2, 96, 0x1111), now, src) {
		t.Fatal("wrong payload type accepted")
	}
	if h.Handle(rawPacket(t, 2, 111, 0x2222), now.Add(time.Millisecond), src) {
		t.Fatal("SSRC change without silence accepted")
	}
	if h.Handle(rawPacket(t, 1, 111, 0x1111), now, src) {
		t.Fatal("duplicate accepted")
	}
	s := h.Snapshot()
	if s.Packets != 1 {
		t.Fatalf("packets=%d want 1", s.Packets)
	}
	if s.BadPT != 1 || s.BadSSRC != 1 {
		t.Fatalf("badPT=%d badSSRC=%d", s.BadPT, s.BadSSRC)
	}
}

func TestHandlerAdoptsNewSSRCAfterSilence(t *testing.T) {
	h := NewHandler(HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		MaxPPS:        1000,
		JitterWindow:  10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	now := time.Now()
	src := "127.0.0.1:1000"
	if !h.Handle(rawPacket(t, 1, 111, 0x1111), now, src) {
		t.Fatal("first packet rejected")
	}
	// Same SSRC, same source: normal.
	if !h.Handle(rawPacket(t, 2, 111, 0x1111), now.Add(time.Millisecond), src) {
		t.Fatal("normal packet rejected")
	}
	// Same SSRC from a different IP:port is sequence poisoning.
	if h.Handle(rawPacket(t, 3, 111, 0x1111), now.Add(2*time.Millisecond), "127.0.0.1:2000") {
		t.Fatal("source change without silence accepted")
	}
	if s := h.Snapshot(); s.BadSource != 1 {
		t.Fatalf("badSource=%d want 1", s.BadSource)
	}
	if !h.Handle(rawPacket(t, 5000, 111, 0x2222), now.Add(3*time.Second), "127.0.0.1:2000") {
		t.Fatal("restarted source rejected")
	}
	s := h.Snapshot()
	if s.SSRC != 0x2222 || s.Source != "127.0.0.1:2000" {
		t.Fatalf("ssrc=%x source=%q", s.SSRC, s.Source)
	}
}
