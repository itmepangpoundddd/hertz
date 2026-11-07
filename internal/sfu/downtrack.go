package sfu

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pionrtp "github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"hertz/internal/rtpengine"
)

// DownTrack is one listener's output track. It decouples the single ingest
// playout goroutine from per-peer SRTP/DTLS work: Broadcast enqueues a copy,
// and a dedicated writer goroutine calls into Pion. A full queue drops the
// oldest packet, preserving low latency instead of growing delay.
type DownTrack struct {
	track    *webrtc.TrackLocalStaticRTP
	queue    chan *pionrtp.Packet
	done     chan struct{}
	closeOne sync.Once
	log      *slog.Logger

	rewriter     RTPRewriter
	sharePayload bool
	ready        atomic.Bool
	enqueued     atomic.Uint64
	written      atomic.Uint64
	bytesSent    atomic.Uint64
	dropped      atomic.Uint64
	writeErrors  atomic.Uint64
	lastWrite    atomic.Int64
}

// NewDownTrack creates a track with a bounded packet queue.
func NewDownTrack(codec webrtc.RTPCodecCapability, id, streamID string, queueSize int, sharePayload bool, log *slog.Logger) (*DownTrack, error) {
	if queueSize < 4 {
		queueSize = 4
	}
	track, err := webrtc.NewTrackLocalStaticRTP(codec, id, streamID)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	d := &DownTrack{
		track:        track,
		queue:        make(chan *pionrtp.Packet, queueSize),
		done:         make(chan struct{}),
		log:          log,
		rewriter:     NewRTPRewriter(),
		sharePayload: sharePayload,
	}
	go d.run()
	return d, nil
}

// Bind implements webrtc.TrackLocal. Pion calls it once negotiation is
// complete; only after that point is it useful to enqueue packets.
func (d *DownTrack) Bind(ctx webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	params, err := d.track.Bind(ctx)
	if err != nil {
		return params, err
	}
	d.ready.Store(true)
	return params, nil
}

// Unbind implements webrtc.TrackLocal.
func (d *DownTrack) Unbind(ctx webrtc.TrackLocalContext) error {
	d.ready.Store(false)
	return d.track.Unbind(ctx)
}

func (d *DownTrack) ID() string                { return d.track.ID() }
func (d *DownTrack) RID() string               { return d.track.RID() }
func (d *DownTrack) StreamID() string          { return d.track.StreamID() }
func (d *DownTrack) Kind() webrtc.RTPCodecType { return d.track.Kind() }
func (d *DownTrack) Codec() webrtc.RTPCodecCapability {
	return d.track.Codec()
}

// WriteRTP enqueues a packet if the listener is bound. It never blocks the
// ingest/fanout goroutine. Returns true if the packet was queued.
func (d *DownTrack) WriteRTP(p *pionrtp.Packet) bool {
	if p == nil || !d.ready.Load() {
		return false
	}
	var cp *pionrtp.Packet
	if d.sharePayload {
		cp = rtpengine.ClonePacketSharedPayload(p)
	} else {
		cp = rtpengine.ClonePacket(p)
	}
	if !d.rewriter.Rewrite(cp) {
		return false
	}
	select {
	case d.queue <- cp:
		d.enqueued.Add(1)
		return true
	default:
	}

	// Drop the oldest queued packet, then retry once. This bounds latency at
	// the cost of a sequence gap, which Opus PLC and browser jitter buffering
	// can conceal. Never block the broadcaster.
	select {
	case <-d.queue:
		d.dropped.Add(1)
	default:
	}
	select {
	case d.queue <- cp:
		d.enqueued.Add(1)
		return true
	default:
		d.dropped.Add(1)
		return false
	}
}

func (d *DownTrack) run() {
	for {
		select {
		case <-d.done:
			return
		case p := <-d.queue:
			if p == nil || !d.ready.Load() {
				continue
			}
			if err := d.track.WriteRTP(p); err != nil {
				d.writeErrors.Add(1)
				d.log.Debug("down-track write failed", "err", err, "track", d.track.ID())
				continue
			}
			d.written.Add(1)
			d.bytesSent.Add(uint64(p.Header.MarshalSize() + len(p.Payload)))
			d.lastWrite.Store(time.Now().UnixNano())
		}
	}
}

// Close stops the writer. Queued packets are released to the GC.
func (d *DownTrack) Close() {
	d.closeOne.Do(func() {
		d.ready.Store(false)
		close(d.done)
	})
}

// DownTrackStats is a snapshot for metrics and the debug endpoint.
type DownTrackStats struct {
	Enqueued     uint64
	Written      uint64
	BytesWritten uint64
	Dropped      uint64
	WriteErrors  uint64
	QueueLen     int
	QueueCap     int
	LastWrite    time.Time
}

func (d *DownTrack) Stats() DownTrackStats {
	var last time.Time
	if ns := d.lastWrite.Load(); ns != 0 {
		last = time.Unix(0, ns)
	}
	return DownTrackStats{
		Enqueued:     d.enqueued.Load(),
		Written:      d.written.Load(),
		BytesWritten: d.bytesSent.Load(),
		Dropped:      d.dropped.Load(),
		WriteErrors:  d.writeErrors.Load(),
		QueueLen:     len(d.queue),
		QueueCap:     cap(d.queue),
		LastWrite:    last,
	}
}
