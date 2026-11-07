// Package signaling implements the HTTP control plane used by browser
// listeners. Media never traverses this path; only SDP and session metadata do.
package signaling

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"

	"hertz/internal/auth"
	"hertz/internal/config"
	"hertz/internal/metrics"
	"hertz/internal/sfu"
)

//go:embed listen.html
var listenPage []byte

// Server exposes the listener API.
type Server struct {
	cfg     config.Config
	mgr     *sfu.Manager
	signer  *auth.Signer
	metrics *metrics.Metrics
	log     *slog.Logger
	turn    *turnREST

	nowPlayingMu sync.RWMutex
	nowPlaying   map[string]nowPlayingEntry
}

func NewServer(cfg config.Config, mgr *sfu.Manager, signer *auth.Signer, m *metrics.Metrics, log *slog.Logger) *Server {
	return &Server{
		cfg:        cfg,
		mgr:        mgr,
		signer:     signer,
		metrics:    m,
		log:        log,
		turn:       newTURNREST(cfg),
		nowPlaying: make(map[string]nowPlayingEntry),
	}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /listen", s.listen)
	mux.HandleFunc("GET /live.ogg", s.liveOgg)
	mux.HandleFunc("GET /v1/public/status", s.publicStatus)
	mux.HandleFunc("POST /v1/nowplaying", s.withPublisherAuth(s.updateNowPlaying))
	mux.HandleFunc("GET /v1/config", s.withSubscriberAuth(s.config))
	mux.HandleFunc("POST /v1/subscribe", s.withSubscriberAuth(s.subscribe))
	mux.HandleFunc("GET /v1/sessions/{id}", s.withSubscriberAuth(s.sessionInfo))
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.withSubscriberAuth(s.closeSession))
	mux.HandleFunc("POST /v1/sessions/{id}/renegotiate", s.withSubscriberAuth(s.renegotiate))
	return s.cors(mux)
}

func (s *Server) listen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(listenPage)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"sessions": s.mgr.Count(),
	})
}

func (s *Server) config(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_id":   s.cfg.StreamID,
		"codec":       "opus/48000/2",
		"ice_servers": s.browserICEServers(claims),
		"max_bitrate": s.cfg.MaxBitrate,
		"jitter_ms":   s.cfg.JitterWindow.Milliseconds(),
	})
}

type subscribeRequest struct {
	StreamID string `json:"stream_id"`
	SDP      string `json:"sdp"`
}

type subscribeResponse struct {
	SessionID  string                    `json:"session_id"`
	StreamID   string                    `json:"stream_id"`
	Answer     webrtc.SessionDescription `json:"answer"`
	ICEServers []config.ICEServer        `json:"ice_servers,omitempty"`
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	start := time.Now()
	var req subscribeRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		s.metrics.SessionErrors.WithLabelValues("bad_request").Inc()
		return
	}
	streamID, err := resolveStreamID(claims, req.StreamID, s.cfg.StreamID)
	if err != nil {
		httpError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := validateOffer(req.SDP, s.cfg.MaxMediaSections); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		s.metrics.SessionErrors.WithLabelValues("bad_sdp").Inc()
		return
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	peer, answer, err := s.mgr.Subscribe(ctx, claims, streamID, offer, s.clientIP(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.metrics.JoinSeconds.Observe(time.Since(start).Seconds())
	writeJSON(w, http.StatusCreated, subscribeResponse{
		SessionID:  peer.ID,
		StreamID:   peer.StreamID,
		Answer:     answer,
		ICEServers: s.browserICEServers(claims),
	})
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	id := r.PathValue("id")
	peer, ok := s.mgr.Get(id)
	if !ok {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if peer.Owner != claims.Subject || !streamAllowed(peer.StreamID, claims) {
		httpError(w, http.StatusForbidden, "session belongs to another subject or stream")
		return
	}
	st := peer.Latest()
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":   peer.ID,
		"stream_id":    peer.StreamID,
		"subject":      peer.Owner,
		"state":        peer.ConnectionState().String(),
		"target_bps":   peer.TargetBitrate(),
		"rtt_ms":       float64(st.RTT) / float64(time.Millisecond),
		"jitter_ms":    float64(st.Jitter) / float64(time.Millisecond),
		"loss_ratio":   st.Loss,
		"outbound_bps": st.OutboundBPS,
		"down_track":   st.DownTrack,
	})
}

type renegotiateRequest struct {
	SDP string `json:"sdp"`
}

func (s *Server) renegotiate(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	id := r.PathValue("id")
	peer, ok := s.mgr.Get(id)
	if !ok {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if peer.Owner != claims.Subject || !streamAllowed(peer.StreamID, claims) {
		httpError(w, http.StatusForbidden, "session belongs to another subject or stream")
		return
	}
	var req renegotiateRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOffer(req.SDP, s.cfg.MaxMediaSections); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	answer, err := s.mgr.Renegotiate(ctx, id, claims.Subject,
		webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP})
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": id,
		"answer":     answer,
	})
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request, claims auth.Claims) {
	id := r.PathValue("id")
	peer, ok := s.mgr.Get(id)
	if !ok {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if peer.Owner != claims.Subject || !streamAllowed(peer.StreamID, claims) {
		httpError(w, http.StatusForbidden, "session belongs to another subject or stream")
		return
	}
	if err := s.mgr.CloseSession(id, claims.Subject); err != nil {
		s.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func streamAllowed(streamID string, claims auth.Claims) bool {
	return claims.Stream == "*" || claims.Stream == streamID
}

// resolveStreamID is the single authorization point for a subscribe request.
// A wildcard token may select any configured stream; a scoped token may only
// use its own stream. The resolved value is passed explicitly to Manager so
// the manager never has to guess from mutable claims.
func resolveStreamID(claims auth.Claims, requested, defaultStream string) (string, error) {
	if requested == "" {
		if claims.Stream == "*" {
			requested = defaultStream
		} else {
			requested = claims.Stream
		}
	}
	if requested == "" || requested == "*" {
		return "", errors.New("stream not permitted by token")
	}
	if claims.Stream != "*" && claims.Stream != requested {
		return "", errors.New("stream not permitted by token")
	}
	return requested, nil
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxSDPBytes+4096)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrExpired), errors.Is(err, auth.ErrSignature),
		errors.Is(err, auth.ErrMalformed), errors.Is(err, auth.ErrAudience):
		httpError(w, http.StatusUnauthorized, "invalid or expired token")
	case errors.Is(err, sfu.ErrCapacity), errors.Is(err, sfu.ErrIPLimit):
		w.Header().Set("Retry-After", "5")
		httpError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, sfu.ErrJoinRate), errors.Is(err, sfu.ErrIPJoinRate):
		w.Header().Set("Retry-After", "1")
		httpError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, sfu.ErrNotFound):
		httpError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, sfu.ErrForbidden):
		httpError(w, http.StatusForbidden, err.Error())
	default:
		s.log.Warn("request failed", "err", err)
		httpError(w, http.StatusInternalServerError, "internal error")
	}
}

// withSubscriberAuth verifies a short-lived bearer token and injects claims.
func (s *Server) withSubscriberAuth(next func(http.ResponseWriter, *http.Request, auth.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		claims, err := s.signer.Verify(token, auth.KindSubscribe, "")
		if err != nil {
			s.writeError(w, err)
			return
		}
		next(w, r, claims)
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// Deliberately do not accept ?token=: query strings leak into access logs
	// and Referer headers.
	return ""
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			clean := parts[:0]
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" {
					clean = append(clean, p)
				}
			}
			if len(clean) > 0 {
				hops := s.cfg.TrustedProxyHops
				if hops < 1 {
					hops = 1
				}
				// The right-most entry was appended by the proxy closest to
				// HERTZ. Walk left by the number of trusted hops; never take
				// an attacker-controlled left-most value by default.
				idx := len(clean) - hops
				if idx < 0 {
					idx = 0
				}
				if ip := net.ParseIP(clean[idx]); ip != nil {
					return ip.String()
				}
				// A trusted proxy should emit an IP. If it did not, do not use
				// an attacker-controlled arbitrary string as a rate-limit key.
				return r.RemoteAddr
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := func(origin string) bool {
		for _, o := range s.cfg.AllowedOrigins {
			if o == "*" || strings.EqualFold(o, origin) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validateOffer rejects video, data channels, and oversized SDP before any
// PeerConnection is allocated.
func validateOffer(raw string, maxSections int) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("empty SDP")
	}
	var sd sdp.SessionDescription
	if err := sd.Unmarshal([]byte(raw)); err != nil {
		return fmt.Errorf("invalid SDP: %w", err)
	}
	if maxSections <= 0 {
		maxSections = 4
	}
	if len(sd.MediaDescriptions) == 0 || len(sd.MediaDescriptions) > maxSections {
		return fmt.Errorf("SDP must contain 1-%d media sections", maxSections)
	}
	for _, md := range sd.MediaDescriptions {
		if md.MediaName.Media != "audio" {
			return fmt.Errorf("unsupported media %q: HERTZ is audio-only", md.MediaName.Media)
		}
		// Listeners may only receive. Rejecting sendrecv/sendonly prevents an
		// untrusted client from making the SFU decrypt and process inbound RTP.
		if _, recv := md.Attribute("recvonly"); !recv {
			if _, inactive := md.Attribute("inactive"); !inactive {
				return fmt.Errorf("each audio section must be recvonly or inactive")
			}
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
