package sfu

import (
	"testing"

	"github.com/pion/rtp"
)

func rtpPkt(seq uint16, ts uint32) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{
		Version:        2,
		SequenceNumber: seq,
		Timestamp:      ts,
		SSRC:           0x11223344,
		PayloadType:    111,
	}, Payload: []byte{0xf8, 0x01}}
}

func TestRTPRewriterContinuity(t *testing.T) {
	r := NewRTPRewriter()
	cases := []struct {
		inSeq, outSeq uint16
		inTS, outTS   uint32
	}{
		{100, 100, 1000, 1000},
		{101, 101, 1960, 1960},
		{103, 103, 3880, 3880}, // loss gap is preserved
		{104, 104, 4840, 4840},
	}
	for i, c := range cases {
		p := rtpPkt(c.inSeq, c.inTS)
		if !r.Rewrite(p) {
			t.Fatalf("case %d rejected", i)
		}
		if p.SequenceNumber != c.outSeq || p.Timestamp != c.outTS {
			t.Fatalf("case %d got seq=%d ts=%d want seq=%d ts=%d",
				i, p.SequenceNumber, p.Timestamp, c.outSeq, c.outTS)
		}
	}
}

func TestRTPRewriterSourceRestart(t *testing.T) {
	r := NewRTPRewriter()
	first := rtpPkt(1000, 100_000)
	r.Rewrite(first)
	second := rtpPkt(1001, 100_960)
	r.Rewrite(second)

	// New source: new sequence and timestamp space. Listener must see +1 / +960.
	restart := rtpPkt(42, 900_000)
	if !r.Rewrite(restart) {
		t.Fatal("restart packet rejected")
	}
	if restart.SequenceNumber != 1002 {
		t.Fatalf("restart seq=%d want 1002", restart.SequenceNumber)
	}
	if restart.Timestamp != 101_920 {
		t.Fatalf("restart ts=%d want 101920", restart.Timestamp)
	}
	next := rtpPkt(43, 900_960)
	r.Rewrite(next)
	if next.SequenceNumber != 1003 || next.Timestamp != 102_880 {
		t.Fatalf("post-restart seq=%d ts=%d want 1003/102880", next.SequenceNumber, next.Timestamp)
	}
}

func TestRTPRewriterTimestampOnlyRestart(t *testing.T) {
	r := NewRTPRewriter()
	r.Rewrite(rtpPkt(10, 10_000))
	r.Rewrite(rtpPkt(11, 10_960))
	p := rtpPkt(12, 5_000_000) // sequence continues, timestamp jumps
	if !r.Rewrite(p) {
		t.Fatal("timestamp restart rejected")
	}
	if p.SequenceNumber != 12 || p.Timestamp != 11_920 {
		t.Fatalf("got %d/%d want 12/11920", p.SequenceNumber, p.Timestamp)
	}
}

func TestRTPRewriterTimestampWrap(t *testing.T) {
	r := NewRTPRewriter()
	r.Rewrite(rtpPkt(1, 0xFFFFFF00))
	p := rtpPkt(2, 0x000002C0) // +960 across uint32 wrap
	r.Rewrite(p)
	if p.Timestamp != 0x000002C0 {
		t.Fatalf("wrapped ts=%#x want 0x2c0", p.Timestamp)
	}
}

func TestRTPRewriterDuplicate(t *testing.T) {
	r := NewRTPRewriter()
	r.Rewrite(rtpPkt(1, 960))
	if r.Rewrite(rtpPkt(1, 960)) {
		t.Fatal("duplicate accepted")
	}
	if r.Rewrite(rtpPkt(0, 0)) {
		t.Fatal("out-of-order accepted")
	}
}
