package signaling

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtp"

	"hertz/internal/auth"
	"hertz/internal/config"
	"hertz/internal/metrics"
	"hertz/internal/sfu"
)

type testSink struct{ closed bool }

func (s *testSink) WriteOpus(payload []byte, timestamp uint32, ssrc uint32) {}
func (s *testSink) Close()                                                  { s.closed = true }

func newPublicTestServer(t *testing.T) (*Server, *sfu.Stream) {
	t.Helper()
	cfg := config.Config{StreamID: "radio", MaxHTTPListeners: 10, AllowedOrigins: []string{"*"}}
	hub := sfu.NewHub(sfu.HubConfig{MaxAverageBitrate: 256_000, MaxHTTPListeners: 10})
	stream := hub.Ensure("radio")
	m := metrics.New()
	signer, err := auth.NewSigner(strings.Repeat("ab", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := sfu.NewManager(cfg, hub, nil, m, logger)
	return NewServer(cfg, mgr, signer, m, logger), stream
}

func TestPublicStatusWithoutSource(t *testing.T) {
	srv, _ := newPublicTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got publicStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Online || got.StreamID != "radio" {
		t.Fatalf("unexpected status: %+v", got)
	}
	if got.Links.AudioOGG != "/live.ogg" || got.Links.Metadata != "/v1/public/status" {
		t.Fatalf("unexpected links: %+v", got.Links)
	}
}

func TestPublicStatusCountsHTTPAndWebRTCPaths(t *testing.T) {
	srv, stream := newPublicTestServer(t)
	srv.metrics.IngestBitrate.WithLabelValues("radio").Set(132000)
	srv.metrics.SourceBitrate.WithLabelValues("radio").Set(256000)
	stream.Broadcast(&rtp.Packet{Payload: []byte{0xf8}})
	if err := stream.AttachSink("http-1", &testSink{}); err != nil {
		t.Fatal(err)
	}
	srv.setNowPlaying("radio", "Song", "Artist")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got publicStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Online || got.BitrateBPS != 132000 || got.TargetBitrateBPS != 256000 {
		t.Fatalf("unexpected source stats: %+v", got)
	}
	if got.Listeners.HTTP != 1 || got.Listeners.WebRTC != 0 || got.Listeners.Total != 1 {
		t.Fatalf("unexpected listeners: %+v", got.Listeners)
	}
	if got.NowPlaying == nil || got.NowPlaying.Title != "Song" || got.NowPlaying.Artist != "Artist" {
		t.Fatalf("unexpected now playing: %+v", got.NowPlaying)
	}
}

func TestNowPlayingRequiresPublisherToken(t *testing.T) {
	srv, _ := newPublicTestServer(t)
	body := `{"title":"Song","artist":"Artist"}`
	post := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/nowplaying", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", rec.Code)
	}

	secret := strings.Repeat("ab", 32)
	signer, err := auth.NewSigner(secret, false)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := signer.Sign(auth.Claims{Subject: "listener", Stream: "radio", Kind: auth.KindSubscribe, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(sub); rec.Code != http.StatusUnauthorized {
		t.Fatalf("subscriber token status=%d body=%s", rec.Code, rec.Body.String())
	}

	other, err := signer.Sign(auth.Claims{Subject: "publisher", Stream: "other", Kind: auth.KindPublish, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(other); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-stream token status=%d body=%s", rec.Code, rec.Body.String())
	}

	pub, err := signer.Sign(auth.Claims{Subject: "publisher", Stream: "radio", Kind: auth.KindPublish, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(pub); rec.Code != http.StatusOK {
		t.Fatalf("publisher token status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := srv.currentNowPlaying("radio"); got == nil || got.Title != "Song" {
		t.Fatalf("now playing not stored: %+v", got)
	}
}

func TestNowPlayingRejectsControlCharacters(t *testing.T) {
	srv, _ := newPublicTestServer(t)
	signer, _ := auth.NewSigner(strings.Repeat("ab", 32), false)
	token, _ := signer.Sign(auth.Claims{Subject: "publisher", Stream: "radio", Kind: auth.KindPublish, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodPost, "/v1/nowplaying", strings.NewReader(`{"title":"bad\nline"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLiveOggStreamsHeadersAndCountsListener(t *testing.T) {
	srv, stream := newPublicTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ts.URL + "/live.ogg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "audio/ogg" {
		t.Fatalf("content-type=%q", got)
	}
	if got := stream.SinkCount(); got != 1 {
		t.Fatalf("sink count=%d want 1", got)
	}

	stream.Broadcast(&rtp.Packet{Header: rtp.Header{Timestamp: 960, SSRC: 1}, Payload: []byte{0xf8}})
	buf := make([]byte, 512)
	n, err := io.ReadAtLeast(resp.Body, buf, 46)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf[:n], []byte("OggS")) || !bytes.Contains(buf[:n], []byte("OpusHead")) {
		t.Fatalf("missing Ogg/Opus headers: %q", buf[:n])
	}
}

func TestLiveOggCapacity(t *testing.T) {
	srv, stream := newPublicTestServer(t)
	for i := 0; i < 10; i++ {
		if err := stream.AttachSink("existing-"+string(rune('a'+i)), &testSink{}); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live.ogg", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
}
