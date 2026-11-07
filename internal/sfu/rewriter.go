package sfu

import "github.com/pion/rtp"

// RTPRewriter gives each listener a continuous RTP sequence/timestamp clock.
// The canonical ingest stream may restart with a new SSRC/sequence/timestamp
// space; without this layer a listener would see a large jump and its NACK /
// jitter buffer would behave badly. SSRC is rewritten by Pion per binding.
//
// RTPRewriter is intentionally per-DownTrack and is not shared.
type RTPRewriter struct {
	initialized bool
	lastInSeq   uint16
	lastInTS    uint32
	lastOutSeq  uint16
	lastOutTS   uint32
	frameTS     uint32
}

// NewRTPRewriter returns a rewriter with a 20 ms Opus default frame duration.
func NewRTPRewriter() RTPRewriter {
	return RTPRewriter{frameTS: 960}
}

// Rewrite mutates p in place. It returns false for duplicate or out-of-order
// packets, which the jitter buffer should already have filtered.
func (r *RTPRewriter) Rewrite(p *rtp.Packet) bool {
	if p == nil {
		return false
	}
	if !r.initialized {
		r.initialized = true
		r.lastInSeq = p.SequenceNumber
		r.lastInTS = p.Timestamp
		r.lastOutSeq = p.SequenceNumber
		r.lastOutTS = p.Timestamp
		if r.frameTS == 0 {
			r.frameTS = 960
		}
		return true
	}

	inSeq, inTS := p.SequenceNumber, p.Timestamp
	seqDelta := int16(inSeq - r.lastInSeq)
	tsDelta := int32(inTS - r.lastInTS)

	if seqDelta == 0 {
		return false
	}
	// A jump larger than 100 packets (2 s at 20 ms) in either direction, or an
	// implausible timestamp delta, means the source restarted. Small negative
	// deltas are late/out-of-order packets and are dropped.
	jump := seqDelta
	if jump < 0 {
		jump = -jump
	}
	if seqDelta < 0 && jump <= 100 {
		return false // late/out-of-order, not a source restart
	}
	restart := jump > 100 || tsDelta <= 0 || tsDelta > 48_000*2
	if restart {
		outSeq := r.lastOutSeq + 1
		outTS := r.lastOutTS + r.frameTS
		r.lastInSeq, r.lastInTS = inSeq, inTS
		r.lastOutSeq, r.lastOutTS = outSeq, outTS
		p.SequenceNumber, p.Timestamp = outSeq, outTS
		return true
	}

	outSeq := r.lastOutSeq + uint16(seqDelta)
	outTS := r.lastOutTS + uint32(tsDelta)
	if seqDelta == 1 && tsDelta > 0 && tsDelta <= 48_000 {
		r.frameTS = uint32(tsDelta)
	}
	r.lastInSeq, r.lastInTS = inSeq, inTS
	r.lastOutSeq, r.lastOutTS = outSeq, outTS
	p.SequenceNumber, p.Timestamp = outSeq, outTS
	return true
}
