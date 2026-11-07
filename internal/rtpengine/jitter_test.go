package rtpengine

import (
	"testing"
	"time"

	"github.com/pion/rtp"
)

func pkt(seq uint16, ts uint32) *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			SequenceNumber: seq,
			Timestamp:      ts,
			SSRC:           0x11223344,
			PayloadType:    111,
		},
		Payload: []byte{0xf8, 0x01, 0x02, 0x03},
	}
}

func drain(t *testing.T, b *JitterBuffer, at time.Time) []uint16 {
	t.Helper()
	var got []uint16
	b.Drain(at, func(p *rtp.Packet) { got = append(got, p.SequenceNumber) })
	return got
}

func TestJitterBufferReordersInitialBurst(t *testing.T) {
	b := NewJitterBuffer(40*time.Millisecond, 48000)
	t0 := time.Unix(100, 0)
	b.Push(pkt(2, 960), t0)
	b.Push(pkt(1, 480), t0.Add(time.Millisecond))
	b.Push(pkt(3, 1440), t0.Add(2*time.Millisecond))

	got := drain(t, b, t0.Add(41*time.Millisecond))
	want := []uint16{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if s := b.Stats(); s.Lost != 0 || s.Late != 0 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestJitterBufferLossAndLate(t *testing.T) {
	b := NewJitterBuffer(20*time.Millisecond, 48000)
	t0 := time.Unix(200, 0)
	b.Push(pkt(10, 0), t0)
	b.Push(pkt(12, 960), t0.Add(time.Millisecond))
	b.Push(pkt(13, 1440), t0.Add(2*time.Millisecond))

	got := drain(t, b, t0.Add(21*time.Millisecond))
	want := []uint16{10, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if s := b.Stats(); s.Lost != 1 {
		t.Fatalf("lost=%d want 1", s.Lost)
	}

	// A packet older than the playout position is late, not emitted.
	if b.Push(pkt(11, 480), t0.Add(22*time.Millisecond)) {
		t.Fatal("late packet was accepted")
	}
	if s := b.Stats(); s.Late != 1 {
		t.Fatalf("late=%d want 1", s.Late)
	}
}

func TestJitterBufferDuplicate(t *testing.T) {
	b := NewJitterBuffer(10*time.Millisecond, 48000)
	t0 := time.Unix(300, 0)
	if !b.Push(pkt(7, 0), t0) {
		t.Fatal("first push rejected")
	}
	if b.Push(pkt(7, 0), t0.Add(time.Millisecond)) {
		t.Fatal("duplicate accepted")
	}
	if s := b.Stats(); s.Duplicates != 1 {
		t.Fatalf("duplicates=%d want 1", s.Duplicates)
	}
}

func TestJitterBufferSequenceWrap(t *testing.T) {
	b := NewJitterBuffer(30*time.Millisecond, 48000)
	t0 := time.Unix(400, 0)
	for i, seq := range []uint16{65534, 65535, 0, 1} {
		b.Push(pkt(seq, uint32(i)*960), t0.Add(time.Duration(i)*time.Millisecond))
	}
	got := drain(t, b, t0.Add(31*time.Millisecond))
	want := []uint16{65534, 65535, 0, 1}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if s := b.Stats(); s.Lost != 0 {
		t.Fatalf("lost=%d want 0", s.Lost)
	}
}

func TestJitterBufferSourceRestart(t *testing.T) {
	b := NewJitterBuffer(20*time.Millisecond, 48000)
	t0 := time.Unix(500, 0)
	b.Push(pkt(100, 0), t0)
	b.Push(pkt(101, 960), t0.Add(time.Millisecond))
	if !b.Push(pkt(5000, 0), t0.Add(2*time.Millisecond)) {
		t.Fatal("restart packet rejected")
	}
	if s := b.Stats(); s.Lost == 0 {
		t.Fatalf("expected old window to be accounted as lost: %+v", s)
	}
	got := drain(t, b, t0.Add(23*time.Millisecond))
	if len(got) != 1 || got[0] != 5000 {
		t.Fatalf("got %v want [5000]", got)
	}
}

func TestJitterTimestampWrap(t *testing.T) {
	b := NewJitterBuffer(0, 48000)
	t0 := time.Unix(600, 0)
	// These timestamps cross the uint32 wrap boundary. Arrival intervals stay
	// 20 ms, so the measured jitter must stay near zero.
	timestamps := []uint32{0xFFFFFF00, 0x000002C0, 0x00000680, 0x00000A40}
	for i, ts := range timestamps {
		seq := uint16(100 + i)
		b.Push(pkt(seq, ts), t0.Add(time.Duration(i)*20*time.Millisecond))
	}
	if s := b.Stats(); s.Jitter > 2*time.Millisecond {
		t.Fatalf("jitter=%v after timestamp wrap; want <2ms", s.Jitter)
	}
}

func TestClonePacketSharedPayloadKeepsHeaderIndependent(t *testing.T) {
	src := pkt(1, 480)
	src.Header.Extension = true
	src.Header.ExtensionProfile = 0xBEDE
	if err := src.Header.SetExtension(3, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	cp := ClonePacketSharedPayload(src)
	if len(cp.Payload) == 0 || &cp.Payload[0] != &src.Payload[0] {
		t.Fatal("shared clone copied the payload")
	}
	src.Header.GetExtension(3)[0] = 0xff
	if cp.Header.GetExtension(3)[0] == 0xff {
		t.Fatal("shared clone shares header extension backing array")
	}
}

func TestClonePacketIndependent(t *testing.T) {
	src := pkt(1, 480)
	src.Header.Extension = true
	src.Header.ExtensionProfile = 0xBEDE
	if err := src.Header.SetExtension(3, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	cp := ClonePacket(src)
	src.Payload[0] = 0xff
	src.Header.GetExtension(3)[0] = 0xff
	cpExt := cp.Header.GetExtension(3)
	if cp.Payload[0] == 0xff || cpExt[0] == 0xff {
		t.Fatal("ClonePacket shares mutable backing arrays")
	}
}
