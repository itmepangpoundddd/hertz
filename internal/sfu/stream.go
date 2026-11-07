package sfu

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	pionrtp "github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// OpusSink receives the same Opus payloads as WebRTC listeners. WriteOpus
// must be non-blocking: it is called inside Stream.Broadcast while the stream
// read lock is held. Implementations should drop or queue rather than block.
type OpusSink interface {
	WriteOpus(payload []byte, timestamp uint32, ssrc uint32)
	Close()
}

// Stream is the fanout point for one logical audio source. Every listener has
// a DownTrack bound to its own PeerConnection; Broadcast walks the map and
// performs a non-blocking enqueue.
type Stream struct {
	ID    string
	Codec webrtc.RTPCodecCapability

	mu        sync.RWMutex
	listeners map[string]*DownTrack
	sinks     map[string]OpusSink
	maxSinks  int

	createdAt  time.Time
	lastPacket atomic.Int64
}

func NewStream(id string, codec webrtc.RTPCodecCapability, maxSinks int) *Stream {
	return &Stream{
		ID:        id,
		Codec:     codec,
		listeners: make(map[string]*DownTrack),
		sinks:     make(map[string]OpusSink),
		maxSinks:  maxSinks,
		createdAt: time.Now(),
	}
}

// ErrSinkCapacity is returned when an HTTP audio listener is over the cap.
var ErrSinkCapacity = errors.New("http audio listener capacity exhausted")

// AttachSink registers an HTTP audio listener.
func (s *Stream) AttachSink(listenerID string, sink OpusSink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxSinks > 0 && len(s.sinks) >= s.maxSinks {
		return ErrSinkCapacity
	}
	s.sinks[listenerID] = sink
	return nil
}

// DetachSink removes and closes an HTTP audio listener.
func (s *Stream) DetachSink(listenerID string) {
	s.mu.Lock()
	sink := s.sinks[listenerID]
	delete(s.sinks, listenerID)
	s.mu.Unlock()
	if sink != nil {
		sink.Close()
	}
}

// SinkCount reports HTTP audio listeners (WebRTC listeners are not included).
func (s *Stream) SinkCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sinks)
}

// Attach registers an existing down track (created by the peer factory).
func (s *Stream) Attach(listenerID string, dt *DownTrack) {
	s.mu.Lock()
	s.listeners[listenerID] = dt
	s.mu.Unlock()
}

// Unsubscribe removes and stops a listener's track.
func (s *Stream) Unsubscribe(listenerID string) {
	s.mu.Lock()
	dt := s.listeners[listenerID]
	delete(s.listeners, listenerID)
	s.mu.Unlock()
	if dt != nil {
		dt.Close()
	}
}

// Broadcast fans one canonical RTP packet out to all listeners. The input
// packet is owned by the caller and must not be mutated here.
func (s *Stream) Broadcast(p *pionrtp.Packet) {
	if p == nil {
		return
	}
	s.lastPacket.Store(time.Now().UnixNano())
	s.mu.RLock()
	for _, dt := range s.listeners {
		dt.WriteRTP(p)
	}
	for _, sink := range s.sinks {
		sink.WriteOpus(p.Payload, p.Timestamp, p.SSRC)
	}
	s.mu.RUnlock()
}

func (s *Stream) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.listeners)
}

func (s *Stream) LastPacket() time.Time {
	if ns := s.lastPacket.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// Close stops every listener track. PeerConnections are owned by the manager
// and are closed separately.
func (s *Stream) Close() {
	s.mu.Lock()
	listeners := s.listeners
	sinks := s.sinks
	s.listeners = make(map[string]*DownTrack)
	s.sinks = make(map[string]OpusSink)
	s.mu.Unlock()
	for _, dt := range listeners {
		dt.Close()
	}
	for _, sink := range sinks {
		sink.Close()
	}
}

// DownTrackStatsByListener returns a copy for debug endpoints.
func (s *Stream) DownTrackStatsByListener() map[string]DownTrackStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]DownTrackStats, len(s.listeners))
	for id, dt := range s.listeners {
		out[id] = dt.Stats()
	}
	return out
}

// Hub owns all streams. v1 creates one stream from configuration; the map
// makes multi-stream ingest an additive change rather than a rewrite.
type Hub struct {
	mu      sync.RWMutex
	streams map[string]*Stream
	cfg     HubConfig
}

type HubConfig struct {
	MaxAverageBitrate int
	MaxHTTPListeners  int
}

func NewHub(cfg HubConfig) *Hub {
	return &Hub{
		streams: make(map[string]*Stream),
		cfg:     cfg,
	}
}

// Ensure returns the stream, creating it on first use.
func (h *Hub) Ensure(id string) *Stream {
	h.mu.RLock()
	s := h.streams[id]
	h.mu.RUnlock()
	if s != nil {
		return s
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s = h.streams[id]; s != nil {
		return s
	}
	s = NewStream(id, OpusCapability(h.cfg.MaxAverageBitrate), h.cfg.MaxHTTPListeners)
	h.streams[id] = s
	return s
}

func (h *Hub) Get(id string) (*Stream, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.streams[id]
	return s, ok
}

func (h *Hub) Close() {
	h.mu.Lock()
	streams := h.streams
	h.streams = make(map[string]*Stream)
	h.mu.Unlock()
	for _, s := range streams {
		s.Close()
	}
}
