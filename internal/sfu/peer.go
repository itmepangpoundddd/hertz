package sfu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/webrtc/v4"

	"hertz/internal/metrics"
)

// PeerStats is the per-listener view used by metrics and the debug endpoint.
type PeerStats struct {
	RTT           time.Duration              `json:"rtt_ms"`
	Jitter        time.Duration              `json:"jitter_ms"`
	Loss          float64                    `json:"loss_ratio"`
	TargetBitrate int                        `json:"target_bitrate_bps"`
	OutboundBPS   int64                      `json:"outbound_bps"`
	PacketsSent   uint64                     `json:"packets_sent"`
	BytesSent     uint64                     `json:"bytes_sent"`
	NoPacketFor   time.Duration              `json:"no_packet_for_ms"`
	State         webrtc.PeerConnectionState `json:"-"`
	DownTrack     DownTrackStats             `json:"down_track"`
	SampledAt     time.Time                  `json:"sampled_at"`
}

// Peer is one subscriber WebRTC session.
type Peer struct {
	ID       string
	StreamID string
	Owner    string
	RemoteIP string

	pc      *webrtc.PeerConnection
	track   *DownTrack
	est     cc.BandwidthEstimator
	metrics *metrics.Metrics
	log     *slog.Logger

	createdAt time.Time
	closed    atomic.Bool
	closeOne  sync.Once
	done      chan struct{}

	onCloseMu   sync.Mutex
	onClose     func()
	onCloseOnce sync.Once

	sdpMu sync.Mutex

	targetBPS atomic.Int64
	state     atomic.Value // webrtc.PeerConnectionState

	statsMu     sync.Mutex
	lastBytes   uint64
	lastStats   time.Time
	lastDropped uint64
	lastWritten uint64
	lastWarn    time.Time
	latest      PeerStats
}

func newPeer(
	id, streamID, owner, remoteIP string,
	pc *webrtc.PeerConnection,
	dt *DownTrack,
	est cc.BandwidthEstimator,
	m *metrics.Metrics,
	log *slog.Logger,
) *Peer {
	p := &Peer{
		ID:        id,
		StreamID:  streamID,
		Owner:     owner,
		RemoteIP:  remoteIP,
		pc:        pc,
		track:     dt,
		est:       est,
		metrics:   m,
		log:       log,
		createdAt: time.Now(),
		done:      make(chan struct{}),
	}
	p.state.Store(webrtc.PeerConnectionStateNew)
	p.pc.OnConnectionStateChange(p.onConnectionState)
	p.pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		p.log.Debug("ice state", "session", p.ID, "state", s.String())
	})
	if est != nil {
		p.targetBPS.Store(int64(est.GetTargetBitrate()))
		est.OnTargetBitrateChange(func(bps int) {
			p.targetBPS.Store(int64(bps))
		})
	}
	return p
}

// SetOnClose registers a cleanup callback. It is invoked exactly once, even
// if the peer closed before registration.
func (p *Peer) SetOnClose(f func()) {
	p.onCloseMu.Lock()
	p.onClose = f
	closed := p.closed.Load()
	p.onCloseMu.Unlock()
	if closed {
		p.fireOnClose()
	}
}

func (p *Peer) fireOnClose() {
	p.onCloseOnce.Do(func() {
		p.onCloseMu.Lock()
		cb := p.onClose
		p.onCloseMu.Unlock()
		if cb != nil {
			cb()
		}
	})
}

// Track exposes the listener down-track so the hub can attach it to a stream.
func (p *Peer) Track() *DownTrack { return p.track }

// Answer applies a browser offer, adds the audio track, and returns a
// non-trickle answer. ICE gathering is bounded by ctx.
func (p *Peer) Answer(ctx context.Context, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	p.sdpMu.Lock()
	defer p.sdpMu.Unlock()

	if offer.Type != webrtc.SDPTypeOffer {
		return webrtc.SessionDescription{}, fmt.Errorf("expected offer, got %s", offer.Type)
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("set remote offer: %w", err)
	}
	if _, err := p.pc.AddTrack(p.track); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("add audio track: %w", err)
	}
	return p.createAnswerLocked(ctx)
}

// Renegotiate applies a new browser offer, typically an ICE restart, without
// adding a second audio track.
func (p *Peer) Renegotiate(ctx context.Context, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	p.sdpMu.Lock()
	defer p.sdpMu.Unlock()

	if offer.Type != webrtc.SDPTypeOffer {
		return webrtc.SessionDescription{}, fmt.Errorf("expected offer, got %s", offer.Type)
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("set remote offer: %w", err)
	}
	return p.createAnswerLocked(ctx)
}

func (p *Peer) createAnswerLocked(ctx context.Context) (webrtc.SessionDescription, error) {
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("create answer: %w", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("set local answer: %w", err)
	}
	select {
	case <-gatherDone:
	case <-ctx.Done():
		return webrtc.SessionDescription{}, fmt.Errorf("ice gathering: %w", ctx.Err())
	}
	local := p.pc.LocalDescription()
	if local == nil {
		return webrtc.SessionDescription{}, errors.New("nil local description after gathering")
	}
	return *local, nil
}

func (p *Peer) onConnectionState(s webrtc.PeerConnectionState) {
	p.state.Store(s)
	if p.metrics != nil {
		p.metrics.ObserveConnectionState(s)
	}
	p.log.Debug("peer state", "session", p.ID, "state", s.String())
	switch s {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		p.Close()
	case webrtc.PeerConnectionStateDisconnected:
		// ICE may recover. The configured ICE timeouts eventually move this to
		// failed; do not tear down a listener on a transient Wi-Fi blip.
	}
}

// Sample reads WebRTC stats and computes the delta send rate. It is safe to
// call periodically from one goroutine.
func (p *Peer) Sample(now time.Time) PeerStats {
	if p.closed.Load() {
		return p.Latest()
	}
	report := p.pc.GetStats()
	prevLatest := p.Latest()

	var (
		rttSec      float64
		jitterSec   float64
		loss        float64
		pionPackets uint64
		pionBytes   uint64
		iceRTT      float64
	)
	for _, s := range report {
		switch stat := s.(type) {
		case webrtc.OutboundRTPStreamStats:
			// HERTZ sends only audio; sum the outbound stream regardless of
			// whether the browser/Pion populated Kind.
			pionPackets += uint64(stat.PacketsSent)
			pionBytes += stat.BytesSent
		case webrtc.RemoteInboundRTPStreamStats:
			if stat.Kind == "audio" || stat.Kind == "" {
				rttSec = stat.RoundTripTime
				jitterSec = stat.Jitter
				loss = stat.FractionLost
			}
		case webrtc.ICECandidatePairStats:
			if stat.State == webrtc.StatsICECandidatePairStateSucceeded && stat.CurrentRoundTripTime > 0 {
				iceRTT = stat.CurrentRoundTripTime
			}
		}
	}
	if rttSec <= 0 {
		rttSec = iceRTT
	}
	if rttSec <= 0 && prevLatest.RTT > 0 {
		rttSec = prevLatest.RTT.Seconds()
	}
	if loss > 1 { // some stacks report 0..255
		loss /= 256
	}
	if loss < 0 {
		loss = 0
	}
	if loss > 1 {
		loss = 1
	}

	// DownTrack counters are authoritative for RTP egress; Pion's outbound
	// stats are a fallback for transports that do not expose BytesSent.
	dtStats := p.track.Stats()
	bytesSent := dtStats.BytesWritten
	packetsSent := dtStats.Written
	if bytesSent == 0 {
		bytesSent = pionBytes
		packetsSent = pionPackets
	}

	p.statsMu.Lock()
	bps := int64(0)
	if !p.lastStats.IsZero() {
		dt := now.Sub(p.lastStats).Seconds()
		if dt > 0 && bytesSent >= p.lastBytes {
			bps = int64(float64(bytesSent-p.lastBytes) * 8 / dt)
		}
	}
	p.lastBytes = bytesSent
	p.lastStats = now
	p.statsMu.Unlock()

	noPacket := time.Duration(0)
	if !dtStats.LastWrite.IsZero() {
		noPacket = now.Sub(dtStats.LastWrite)
	}

	st := PeerStats{
		RTT:           time.Duration(rttSec * float64(time.Second)),
		Jitter:        time.Duration(jitterSec * float64(time.Second)),
		Loss:          loss,
		TargetBitrate: int(p.targetBPS.Load()),
		OutboundBPS:   bps,
		PacketsSent:   packetsSent,
		BytesSent:     bytesSent,
		NoPacketFor:   noPacket,
		State:         p.ConnectionState(),
		DownTrack:     dtStats,
		SampledAt:     now,
	}
	p.statsMu.Lock()
	p.latest = st
	p.statsMu.Unlock()
	return st
}

// Latest returns the most recent sample without touching the PC.
func (p *Peer) Latest() PeerStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	return p.latest
}

// RunStats samples at a fixed interval until the peer closes.
func (p *Peer) RunStats(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case now := <-t.C:
			st := p.Sample(now)
			if p.metrics != nil {
				p.metrics.ObservePeer(p.StreamID, metrics.PeerSample{
					RTT:           st.RTT,
					Jitter:        st.Jitter,
					Loss:          st.Loss,
					TargetBitrate: st.TargetBitrate,
					OutboundBPS:   st.OutboundBPS,
					NoPacketFor:   st.NoPacketFor,
				})
				p.statsMu.Lock()
				dropDelta := st.DownTrack.Dropped - p.lastDropped
				writeDelta := st.DownTrack.Written - p.lastWritten
				p.lastDropped = st.DownTrack.Dropped
				p.lastWritten = st.DownTrack.Written
				p.statsMu.Unlock()
				if dropDelta > 0 {
					p.metrics.FanoutDropped.WithLabelValues(p.StreamID).Add(float64(dropDelta))
				}
				if writeDelta > 0 {
					p.metrics.FanoutWritten.WithLabelValues(p.StreamID).Add(float64(writeDelta))
				}
			}
			if st.Loss > 0.05 || st.RTT > 300*time.Millisecond {
				p.statsMu.Lock()
				shouldLog := now.Sub(p.lastWarn) > 30*time.Second
				if shouldLog {
					p.lastWarn = now
				}
				p.statsMu.Unlock()
				if shouldLog {
					p.log.Warn("listener path degraded",
						"session", p.ID, "loss", st.Loss, "rtt", st.RTT,
						"target_bps", st.TargetBitrate, "out_bps", st.OutboundBPS)
				}
			}
			if p.ConnectionState() == webrtc.PeerConnectionStateClosed {
				return
			}
		}
	}
}

func (p *Peer) ConnectionState() webrtc.PeerConnectionState {
	if v, ok := p.state.Load().(webrtc.PeerConnectionState); ok {
		return v
	}
	return webrtc.PeerConnectionStateNew
}

func (p *Peer) CreatedAt() time.Time { return p.createdAt }

// TargetBitrate is the latest GCC estimate for this listener.
func (p *Peer) TargetBitrate() int { return int(p.targetBPS.Load()) }

// Close is idempotent and invokes the manager cleanup callback exactly once.
func (p *Peer) Close() {
	p.closeOne.Do(func() {
		p.closed.Store(true)
		close(p.done)
		p.track.Close()
		_ = p.pc.Close()
		p.fireOnClose()
	})
}

// Renegotiate is exposed for the session manager's ICE-restart endpoint.
func (p *Peer) IsClosed() bool { return p.closed.Load() }
