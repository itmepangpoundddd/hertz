package sfu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"golang.org/x/time/rate"

	"hertz/internal/auth"
	"hertz/internal/config"
	"hertz/internal/metrics"
)

var (
	ErrCapacity   = errors.New("session capacity exhausted")
	ErrIPLimit    = errors.New("per-ip session limit exceeded")
	ErrJoinRate   = errors.New("join rate limit exceeded")
	ErrIPJoinRate = errors.New("per-ip join rate limit exceeded")
	ErrNotFound   = errors.New("session not found")
	ErrForbidden  = errors.New("session belongs to another subject")
)

// Manager owns sessions and enforces admission limits. A slot is reserved
// before the expensive DTLS/ICE allocation, then released on any failure.
type Manager struct {
	cfg     config.Config
	hub     *Hub
	factory *PeerFactory
	metrics *metrics.Metrics
	log     *slog.Logger

	mu                sync.RWMutex
	peers             map[string]*Peer
	perIP             map[string]int
	total             int
	joinRate          *rate.Limiter
	ipLimiterMu       sync.Mutex
	ipLimiters        map[string]*rate.Limiter
	ipLimiterLastSeen map[string]time.Time
	baseCtx           context.Context
	cancel            context.CancelFunc

	abr *ABRController
}

func NewManager(cfg config.Config, hub *Hub, factory *PeerFactory, m *metrics.Metrics, log *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:               cfg,
		hub:               hub,
		factory:           factory,
		metrics:           m,
		log:               log,
		peers:             make(map[string]*Peer),
		perIP:             make(map[string]int),
		joinRate:          rate.NewLimiter(rate.Limit(cfg.JoinRatePerSecond), cfg.JoinBurst),
		ipLimiters:        make(map[string]*rate.Limiter),
		ipLimiterLastSeen: make(map[string]time.Time),
		baseCtx:           ctx,
		cancel:            cancel,
		abr:               NewABRController(cfg, m, log),
	}
}

// Subscribe creates a peer, applies the browser offer, and attaches the
// listener to a stream. The returned answer is a complete (non-trickle) SDP.
func (m *Manager) Subscribe(ctx context.Context, claims auth.Claims, streamID string, offer webrtc.SessionDescription, remoteIP string) (*Peer, webrtc.SessionDescription, error) {
	stream, ok := m.hub.Get(streamID)
	if !ok {
		return nil, webrtc.SessionDescription{}, fmt.Errorf("unknown stream %q", streamID)
	}
	if err := m.reserve(remoteIP); err != nil {
		return nil, webrtc.SessionDescription{}, err
	}

	id := auth.NewID()
	peer, err := m.factory.New(id, streamID, claims.Subject, remoteIP)
	if err != nil {
		m.release(remoteIP)
		m.metrics.SessionErrors.WithLabelValues("factory").Inc()
		return nil, webrtc.SessionDescription{}, err
	}
	// Register cleanup before signaling so an early ICE/DTLS failure cannot
	// leak an admission slot. remove is idempotent by construction: Peer.Close
	// runs exactly once.
	peer.SetOnClose(func() { m.remove(id, remoteIP, streamID) })

	answer, err := peer.Answer(ctx, offer)
	if err != nil {
		peer.Close() // releases the admission slot via onClose
		m.metrics.SessionErrors.WithLabelValues("answer").Inc()
		return nil, webrtc.SessionDescription{}, err
	}

	stream.Attach(id, peer.Track())

	m.mu.Lock()
	if peer.IsClosed() {
		m.mu.Unlock()
		stream.Unsubscribe(id)
		return nil, webrtc.SessionDescription{}, errors.New("peer closed during setup")
	}
	m.peers[id] = peer
	m.metrics.SessionsTotal.Inc()
	m.metrics.SessionsActive.Inc()
	m.mu.Unlock()

	m.metrics.Listeners.WithLabelValues(streamID).Set(float64(stream.Count()))

	go peer.RunStats(m.baseCtx, m.cfg.StatsInterval)
	m.log.Info("listener attached",
		"session", id, "stream", streamID, "subject", claims.Subject, "ip", remoteIP)
	return peer, answer, nil
}

func (m *Manager) reserve(ip string) error {
	// Per-IP first: a single client must not consume the global join budget
	// before its own limiter rejects it.
	if !m.ipLimiter(ip).Allow() {
		m.metrics.SessionErrors.WithLabelValues("ip_join_rate").Inc()
		return ErrIPJoinRate
	}
	if !m.joinRate.Allow() {
		m.metrics.SessionErrors.WithLabelValues("join_rate").Inc()
		return ErrJoinRate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.total >= m.cfg.MaxSessions {
		m.metrics.SessionErrors.WithLabelValues("capacity").Inc()
		return ErrCapacity
	}
	if m.cfg.MaxSessionsPerIP > 0 && m.perIP[ip] >= m.cfg.MaxSessionsPerIP {
		m.metrics.SessionErrors.WithLabelValues("ip_limit").Inc()
		return ErrIPLimit
	}
	m.total++
	m.perIP[ip]++
	return nil
}

func (m *Manager) release(ip string) {
	m.mu.Lock()
	m.total--
	if m.perIP[ip] > 1 {
		m.perIP[ip]--
	} else {
		delete(m.perIP, ip)
	}
	m.mu.Unlock()
}

func (m *Manager) ipLimiter(ip string) *rate.Limiter {
	m.ipLimiterMu.Lock()
	defer m.ipLimiterMu.Unlock()
	l := m.ipLimiters[ip]
	if l == nil {
		l = rate.NewLimiter(rate.Limit(m.cfg.IPJoinRatePerSecond), m.cfg.IPJoinBurst)
		m.ipLimiters[ip] = l
	}
	m.ipLimiterLastSeen[ip] = time.Now()
	return l
}

// cleanupIPLimiters keeps the per-IP limiter map bounded without resetting a
// client's burst immediately after it disconnects. An entry is removed only
// after one hour with no active sessions and no join attempts.
func (m *Manager) cleanupIPLimiters(now time.Time) {
	m.mu.RLock()
	active := make(map[string]bool, len(m.perIP))
	for ip := range m.perIP {
		active[ip] = true
	}
	m.mu.RUnlock()

	m.ipLimiterMu.Lock()
	defer m.ipLimiterMu.Unlock()
	for ip, seen := range m.ipLimiterLastSeen {
		if active[ip] {
			continue
		}
		if now.Sub(seen) > time.Hour {
			delete(m.ipLimiters, ip)
			delete(m.ipLimiterLastSeen, ip)
		}
	}
}

func (m *Manager) remove(id, ip, streamID string) {
	m.mu.Lock()
	peer := m.peers[id]
	delete(m.peers, id)
	if m.total > 0 {
		m.total--
	}
	if m.perIP[ip] > 1 {
		m.perIP[ip]--
	} else {
		delete(m.perIP, ip)
	}
	m.mu.Unlock()

	if s, ok := m.hub.Get(streamID); ok {
		s.Unsubscribe(id)
		m.metrics.Listeners.WithLabelValues(streamID).Set(float64(s.Count()))
	}
	if peer == nil {
		// The callback raced with registration; the admission slot is already
		// released and the session was never counted as active.
		return
	}
	m.metrics.SessionsActive.Dec()
	m.log.Info("listener detached", "session", id, "stream", streamID)
}

// CloseSession closes a session if the caller owns it.
func (m *Manager) CloseSession(id, subject string) error {
	m.mu.RLock()
	p := m.peers[id]
	m.mu.RUnlock()
	if p == nil {
		return ErrNotFound
	}
	if p.Owner != subject {
		return ErrForbidden
	}
	p.Close()
	return nil
}

// Renegotiate applies an ICE-restart offer to an existing session.
func (m *Manager) Renegotiate(ctx context.Context, id, subject string, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	m.mu.RLock()
	p := m.peers[id]
	m.mu.RUnlock()
	if p == nil {
		return webrtc.SessionDescription{}, ErrNotFound
	}
	if p.Owner != subject {
		return webrtc.SessionDescription{}, ErrForbidden
	}
	return p.Renegotiate(ctx, offer)
}

func (m *Manager) Get(id string) (*Peer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.peers[id]
	return p, ok
}

// Snapshot returns a shallow copy of active peers for the debug endpoint and
// the ABR aggregator.
func (m *Manager) Snapshot() []*Peer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Peer, 0, len(m.peers))
	for _, p := range m.peers {
		out = append(out, p)
	}
	return out
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.total
}

// Stream returns the fanout stream used by HTTP audio and public-status
// handlers. It is a read-only lookup; the Hub owns the stream lifetime.
func (m *Manager) Stream(id string) (*Stream, bool) {
	return m.hub.Get(id)
}

// StartJanitor reaps sessions past their TTL and terminal peer connections.
func (m *Manager) StartJanitor(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			m.cleanupIPLimiters(now)
			for _, p := range m.Snapshot() {
				if now.Sub(p.CreatedAt()) > m.cfg.SessionTTL {
					m.log.Info("session ttl expired", "session", p.ID)
					p.Close()
					continue
				}
				switch p.ConnectionState() {
				case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
					p.Close()
				}
			}
		}
	}
}

// RunABR periodically asks the source encoder for a bitrate that matches the
// median listener path. Per-listener GCC still paces each sender separately.
func (m *Manager) RunABR(ctx context.Context) {
	m.abr.Run(ctx, m)
}

func (m *Manager) CloseAll() {
	m.cancel()
	for _, p := range m.Snapshot() {
		p.Close()
	}
	m.hub.Close()
}
