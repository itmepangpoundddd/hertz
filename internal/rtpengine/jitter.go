// Package rtpengine implements the ingest-side RTP handling: validation,
// packet cloning, and a small sequence-ordered jitter buffer.
package rtpengine

import (
	"math"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// ClonePacket creates an independent RTP packet. The ingest read buffer is
// reused, so every packet that crosses a goroutine boundary must be copied.
func ClonePacket(p *rtp.Packet) *rtp.Packet {
	return clonePacket(p, false)
}

// ClonePacketSharedPayload is the experimental fanout fast path: header
// extensions/CSRC are copied because DownTrack and TWCC mutate headers, while
// the Opus payload is shared read-only. Callers must not mutate payload bytes.
// It is off by default until a race test against the full Pion pipeline proves
// safety and benchmarks show a win.
func ClonePacketSharedPayload(p *rtp.Packet) *rtp.Packet {
	return clonePacket(p, true)
}

func clonePacket(p *rtp.Packet, sharePayload bool) *rtp.Packet {
	if p == nil {
		return nil
	}
	cp := &rtp.Packet{Header: p.Header}
	if sharePayload {
		cp.Payload = p.Payload
	} else {
		cp.Payload = append([]byte(nil), p.Payload...)
	}
	if len(p.Header.CSRC) > 0 {
		cp.Header.CSRC = append([]uint32(nil), p.Header.CSRC...)
	}
	// rtp.Extension has unexported fields. Rebuild extensions through the
	// public API so each payload gets its own backing array.
	ids := p.Header.GetExtensionIDs()
	if len(ids) > 0 {
		cp.Header.Extensions = nil
		cp.Header.Extension = true
		cp.Header.ExtensionProfile = p.Header.ExtensionProfile
		for _, id := range ids {
			payload := p.Header.GetExtension(id)
			_ = cp.Header.SetExtension(id, append([]byte(nil), payload...))
		}
	}
	return cp
}

// StripExtensions removes RTP header extensions. The SFU adds only the
// extensions it negotiated (currently transport-cc), so untrusted source
// extensions must not leak to listeners.
func StripExtensions(p *rtp.Packet) {
	if p == nil {
		return
	}
	p.Header.ClearExtensions()
	p.Header.CSRC = nil
}

// Stats is a point-in-time snapshot of the ingest jitter buffer.
type Stats struct {
	Received   uint64
	Emitted    uint64
	Lost       uint64
	Late       uint64
	Duplicates uint64
	Jitter     time.Duration
	Buffered   int
}

// JitterBuffer reorders RTP packets and releases them at a fixed playout
// delay. It is deliberately small: audio packets arrive at 50 pps, so a
// 40–80 ms window is enough to absorb LAN/WAN reordering without adding a
// meaningful amount of end-to-end latency.
type JitterBuffer struct {
	mu        sync.Mutex
	window    time.Duration
	clockRate float64

	packets map[uint16]*entry
	next    uint16
	started bool
	startAt time.Time

	lastArrival   time.Time
	lastTimestamp uint32
	haveJitter    bool
	jitter        float64

	received   uint64
	emitted    uint64
	lost       uint64
	late       uint64
	duplicates uint64

	maxGap int
}

type entry struct {
	pkt *rtp.Packet
	at  time.Time
}

// NewJitterBuffer creates a buffer. window is the reorder/playout delay;
// packets are held at least that long before their sequence is eligible.
func NewJitterBuffer(window time.Duration, clockRate uint32) *JitterBuffer {
	if window < 0 {
		window = 0
	}
	return &JitterBuffer{
		window:    window,
		clockRate: float64(clockRate),
		packets:   make(map[uint16]*entry, 64),
		maxGap:    2048,
	}
}

// Push inserts a packet. It returns false for duplicates, late packets, and
// packets that were rejected by a source reset guard.
func (b *JitterBuffer) Push(pkt *rtp.Packet, at time.Time) bool {
	if pkt == nil || len(pkt.Payload) == 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	seq := pkt.SequenceNumber
	if !b.started {
		b.started = true
		b.next = seq
		b.startAt = at.Add(b.window)
		b.packets[seq] = &entry{pkt: pkt, at: at}
		b.updateJitterLocked(pkt, at)
		b.received++
		return true
	}

	if seqLess(seq, b.next) {
		// The very first datagram may itself be reordered. During the initial
		// playout window, accept a lower sequence number and move the start
		// point backwards; afterwards it is genuinely late.
		if b.emitted == 0 && at.Before(b.startAt) && int16(b.next-seq) < int16(b.maxGap) {
			b.next = seq
			b.packets[seq] = &entry{pkt: pkt, at: at}
			b.updateJitterLocked(pkt, at)
			b.received++
			return true
		}
		b.late++
		return false
	}
	if _, exists := b.packets[seq]; exists {
		b.duplicates++
		return false
	}

	gap := int16(seq - b.next)
	if gap > int16(b.maxGap) {
		// The source restarted or its clock jumped. Drop the old window and
		// adopt the new sequence space rather than emitting thousands of
		// bogus "lost" packets.
		b.resetLocked(seq, at)
		b.packets[seq] = &entry{pkt: pkt, at: at}
		b.updateJitterLocked(pkt, at)
		b.received++
		return true
	}

	b.packets[seq] = &entry{pkt: pkt, at: at}
	b.updateJitterLocked(pkt, at)
	b.received++
	return true
}

// Drain emits every packet that is eligible at now. emit is called without the
// internal lock held, so a slow consumer cannot block ingest.
func (b *JitterBuffer) Drain(now time.Time, emit func(*rtp.Packet)) int {
	b.mu.Lock()
	if !b.started || now.Before(b.startAt) {
		b.mu.Unlock()
		return 0
	}

	var out []*rtp.Packet
	for len(out) < 512 {
		if e, ok := b.packets[b.next]; ok {
			delete(b.packets, b.next)
			out = append(out, e.pkt)
			b.emitted++
			b.next++
			continue
		}

		// The expected packet is missing. Release the lowest later packet once
		// it has waited out the reorder window, and account for the gap as loss.
		var best uint16
		found := false
		for seq, e := range b.packets {
			if !seqLess(b.next, seq) {
				continue
			}
			if e.at.Add(b.window).After(now) {
				continue
			}
			if !found || seqLess(seq, best) {
				best, found = seq, true
			}
		}
		if !found {
			break
		}
		gap := int16(best - b.next)
		if gap <= 0 || int(gap) > b.maxGap {
			b.resetLocked(best, now)
			break
		}
		b.lost += uint64(gap)
		b.next = best
	}
	b.mu.Unlock()

	for _, p := range out {
		emit(p)
	}
	return len(out)
}

// Run drives Drain until ctx is cancelled.
func (b *JitterBuffer) Run(done <-chan struct{}, tick time.Duration, emit func(*rtp.Packet)) {
	if tick <= 0 {
		tick = 5 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			b.Drain(now, emit)
		}
	}
}

// Reset clears buffered packets and restarts at seq. Used after a source SSRC
// change or an unrecoverable sequence jump.
func (b *JitterBuffer) Reset(seq uint16, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetLocked(seq, at)
}

func (b *JitterBuffer) resetLocked(seq uint16, at time.Time) {
	b.lost += uint64(len(b.packets))
	b.packets = make(map[uint16]*entry, 64)
	b.next = seq
	b.started = true
	b.startAt = at.Add(b.window)
	b.lastArrival = time.Time{}
	b.lastTimestamp = 0
	b.haveJitter = false
	b.jitter = 0
}

// Stats returns a consistent snapshot.
func (b *JitterBuffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{
		Received:   b.received,
		Emitted:    b.emitted,
		Lost:       b.lost,
		Late:       b.late,
		Duplicates: b.duplicates,
		Jitter:     time.Duration(b.jitter * float64(time.Second)),
		Buffered:   len(b.packets),
	}
}

func (b *JitterBuffer) updateJitterLocked(pkt *rtp.Packet, at time.Time) {
	if b.clockRate <= 0 {
		return
	}
	if !b.haveJitter {
		b.lastArrival = at
		b.lastTimestamp = pkt.Timestamp
		b.haveJitter = true
		return
	}
	// RFC 3550: D(i,j) = (Rj - Ri) - (Sj - Si). Compute the RTP delta in
	// uint32 arithmetic so the RTP timestamp wrap at ~24.9 h does not create a
	// false multi-hour transit jump.
	arrivalDelta := at.Sub(b.lastArrival).Seconds()
	rtpDelta := float64(uint32(pkt.Timestamp-b.lastTimestamp)) / b.clockRate
	d := math.Abs(arrivalDelta - rtpDelta)
	b.jitter += (d - b.jitter) / 16.0
	b.lastArrival = at
	b.lastTimestamp = pkt.Timestamp
}

// seqLess reports whether a is before b in 16-bit wrapping sequence space.
func seqLess(a, b uint16) bool { return int16(a-b) < 0 }
