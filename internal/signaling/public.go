package signaling

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"hertz/internal/auth"
	"hertz/internal/metrics"
	"hertz/internal/oggopus"
	"hertz/internal/sfu"
)

const maxNowPlayingField = 200

type nowPlayingView struct {
	Title     string    `json:"title"`
	Artist    string    `json:"artist"`
	UpdatedAt time.Time `json:"updated_at"`
}

type nowPlayingEntry struct {
	view nowPlayingView
}

type publicListenerCounts struct {
	WebRTC int `json:"webrtc"`
	HTTP   int `json:"http"`
	Total  int `json:"total"`
}

type publicLinks struct {
	AudioOGG string `json:"audio_ogg"`
	WebRTC   string `json:"webrtc"`
	Metadata string `json:"metadata"`
}

type publicStatusResponse struct {
	StreamID         string               `json:"stream_id"`
	Online           bool                 `json:"online"`
	Listeners        publicListenerCounts `json:"listeners"`
	BitrateBPS       float64              `json:"bitrate_bps"`
	TargetBitrateBPS float64              `json:"target_bitrate_bps"`
	NowPlaying       *nowPlayingView      `json:"now_playing,omitempty"`
	Links            publicLinks          `json:"links"`
}

func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	stream, ok := s.mgr.Stream(s.cfg.StreamID)
	if !ok {
		httpError(w, http.StatusNotFound, "stream not found")
		return
	}
	last := stream.LastPacket()
	online := !last.IsZero() && time.Since(last) < 8*time.Second
	var bitrate, target float64
	if s.metrics != nil {
		bitrate = metrics.GaugeValue(s.metrics.IngestBitrate.WithLabelValues(s.cfg.StreamID))
		target = metrics.GaugeValue(s.metrics.SourceBitrate.WithLabelValues(s.cfg.StreamID))
	}
	webrtc := stream.Count()
	httpListeners := stream.SinkCount()
	resp := publicStatusResponse{
		StreamID:         s.cfg.StreamID,
		Online:           online,
		Listeners:        publicListenerCounts{WebRTC: webrtc, HTTP: httpListeners, Total: webrtc + httpListeners},
		BitrateBPS:       bitrate,
		TargetBitrateBPS: target,
		NowPlaying:       s.currentNowPlaying(s.cfg.StreamID),
		Links: publicLinks{
			AudioOGG: "/live.ogg",
			WebRTC:   "/listen",
			Metadata: "/v1/public/status",
		},
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) updateNowPlaying(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	var req struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	artist := strings.TrimSpace(req.Artist)
	if !validNowPlayingText(title) || !validNowPlayingText(artist) {
		httpError(w, http.StatusBadRequest, "title and artist must be at most 200 characters without control characters")
		return
	}
	view := s.setNowPlaying(s.cfg.StreamID, title, artist)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"stream_id":   s.cfg.StreamID,
		"now_playing": view,
		"updated_by":  claims.Subject,
	})
}

func validNowPlayingText(value string) bool {
	if utf8.RuneCountInString(value) > maxNowPlayingField {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (s *Server) setNowPlaying(stream, title, artist string) *nowPlayingView {
	s.nowPlayingMu.Lock()
	defer s.nowPlayingMu.Unlock()
	if title == "" && artist == "" {
		delete(s.nowPlaying, stream)
		return nil
	}
	view := nowPlayingView{
		Title:     title,
		Artist:    artist,
		UpdatedAt: time.Now().UTC(),
	}
	s.nowPlaying[stream] = nowPlayingEntry{view: view}
	return &view
}

func (s *Server) currentNowPlaying(stream string) *nowPlayingView {
	s.nowPlayingMu.RLock()
	defer s.nowPlayingMu.RUnlock()
	entry, ok := s.nowPlaying[stream]
	if !ok {
		return nil
	}
	view := entry.view
	return &view
}

func (s *Server) withPublisherAuth(next func(http.ResponseWriter, *http.Request, auth.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		claims, err := s.signer.Verify(token, auth.KindPublish, s.cfg.StreamID)
		if err != nil {
			s.writeError(w, err)
			return
		}
		next(w, r, claims)
	}
}

// liveOgg serves an Ogg/Opus stream for VLC and custom HTTP players. It taps
// the same RTP payloads that WebRTC listeners receive; no decode or re-encode
// happens here.
func (s *Server) liveOgg(w http.ResponseWriter, r *http.Request) {
	stream, ok := s.mgr.Stream(s.cfg.StreamID)
	if !ok {
		httpError(w, http.StatusNotFound, "stream not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	sink := newOGGSink()
	sinkID := auth.NewID()
	if err := stream.AttachSink(sinkID, sink); err != nil {
		if errors.Is(err, sfu.ErrSinkCapacity) {
			w.Header().Set("Retry-After", "5")
			httpError(w, http.StatusServiceUnavailable, "HTTP listener capacity exhausted")
			return
		}
		httpError(w, http.StatusInternalServerError, "could not attach audio listener")
		return
	}
	defer stream.DetachSink(sinkID)

	w.Header().Set("Content-Type", "audio/ogg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The global server WriteTimeout is intended for short signaling requests.
	// Ogg listeners are long-lived, so clear the deadline for this response.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)

	writer := oggopus.NewWriter(w, oggSerial())
	if err := writer.WriteHeaders(); err != nil {
		return
	}
	flusher.Flush()

	idle := time.NewTimer(45 * time.Second)
	defer idle.Stop()
	for {
		select {
		case pkt := <-sink.packets:
			if err := writer.WritePacket(pkt.payload, pkt.timestamp); err != nil {
				return
			}
			flusher.Flush()
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(45 * time.Second)
		case <-sink.done:
			_ = writer.Close()
			flusher.Flush()
			return
		case <-idle.C:
			_ = writer.Close()
			flusher.Flush()
			return
		case <-r.Context().Done():
			return
		}
	}
}

type oggPacket struct {
	payload   []byte
	timestamp uint32
}

type oggSink struct {
	packets chan oggPacket
	done    chan struct{}
	once    sync.Once
}

func newOGGSink() *oggSink {
	return &oggSink{
		packets: make(chan oggPacket, 64),
		done:    make(chan struct{}),
	}
}

func (s *oggSink) WriteOpus(payload []byte, timestamp uint32, ssrc uint32) {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case s.packets <- oggPacket{payload: cp, timestamp: timestamp}:
	default:
		// Slow HTTP client: drop rather than block the WebRTC fanout.
	}
}

func (s *oggSink) Close() {
	s.once.Do(func() { close(s.done) })
}

func oggSerial() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return binary.LittleEndian.Uint32(b[:])
	}
	return uint32(time.Now().UnixNano())
}
