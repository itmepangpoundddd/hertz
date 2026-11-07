package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hertz/internal/auth"
)

func newTestApp(t *testing.T, complete bool) (*App, Settings) {
	t.Helper()
	t.Setenv("HERTZ_ADMIN_DATA", t.TempDir())
	t.Setenv("HERTZ_ADMIN_ENGINE", "")
	s := validSettings()
	s.SetupComplete = complete
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := NewApp(s, "test", logger)
	t.Cleanup(func() { _ = app.engine.Stop() })
	return app, s
}

func adminRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1:8090"
	req.RemoteAddr = "127.0.0.1:54321"
	return req
}

func TestHealthEndpoint(t *testing.T) {
	app, _ := newTestApp(t, true)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["app"] != "hertz-admin" {
		t.Fatalf("app=%v", body["app"])
	}
}

func TestAdminRejectsForeignHost(t *testing.T) {
	app, _ := newTestApp(t, true)
	req := adminRequest(http.MethodGet, "/api/state", nil)
	req.Host = "attacker.example:8090"
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
}

func TestAdminRejectsNonLoopbackPeer(t *testing.T) {
	app, _ := newTestApp(t, true)
	req := adminRequest(http.MethodGet, "/api/state", nil)
	req.RemoteAddr = "192.168.1.50:5555"
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
}

func TestPostRequiresAdminHeader(t *testing.T) {
	app, _ := newTestApp(t, true)
	req := adminRequest(http.MethodPost, "/api/settings", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
}

func TestStateDoesNotLeakSecret(t *testing.T) {
	app, s := newTestApp(t, true)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, "/api/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), s.AuthSecret) {
		t.Fatal(" /api/state leaked the auth secret")
	}
	if !strings.Contains(rec.Body.String(), `"auth_secret_set":true`) {
		t.Fatal("/api/state did not report auth_secret_set")
	}
}

func TestShareTokenVerifies(t *testing.T) {
	app, s := newTestApp(t, true)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, "/api/share?ttl=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		URL       string `json:"url"`
		Token     string `json:"token"`
		TTLHours  int    `json:"ttl_hours"`
		QR        string `json:"qr"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TTLHours != 2 {
		t.Fatalf("ttl_hours=%d, want 2", body.TTLHours)
	}
	if !strings.HasPrefix(body.QR, "data:image/png;base64,") {
		t.Fatal("missing QR data URI")
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/listen" {
		t.Fatalf("link path=%q", u.Path)
	}
	if !strings.HasPrefix(u.Fragment, "t=") {
		t.Fatalf("fragment=%q", u.Fragment)
	}
	token := strings.TrimPrefix(u.Fragment, "t=")
	if token != body.Token {
		t.Fatal("token in URL and JSON differ")
	}
	signer, err := auth.NewSigner(s.AuthSecret, false)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Verify(token, auth.KindSubscribe, s.StreamID)
	if err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	if claims.Stream != s.StreamID || !strings.HasPrefix(claims.Subject, "listener-") {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	remaining := time.Until(time.Unix(claims.ExpiresAt, 0))
	if remaining < time.Hour || remaining > 3*time.Hour {
		t.Fatalf("token expiry is %v, want about 2h", remaining)
	}
}

func TestAdminUIPagesAreEnglishWithEmbedExample(t *testing.T) {
	app, _ := newTestApp(t, true)
	pages := []struct {
		target string
		want   []string
	}{
		{"/", []string{`id="embed-code"`, `id="copy-embed"`, `searchParams.set("embed", "1")`, `id="audio-url"`, `id="metadata-url"`, `id="np-title"`}},
		{"/settings", []string{`html lang="en"`, `id="submit"`}},
		{"/assets/app.js", []string{`Intl.NumberFormat("en-US"`}},
	}
	for _, tc := range pages {
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, tc.target, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", tc.target, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, r := range body {
			if r >= 0x0E00 && r <= 0x0E7F {
				t.Fatalf("%s still contains Thai text near %q", tc.target, string(r))
			}
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Fatalf("%s does not contain %q", tc.target, want)
			}
		}
	}
}

func TestAdminUIAssetsAreNotCached(t *testing.T) {
	app, _ := newTestApp(t, true)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
}

func TestStatusIncludesPublicLinksAndNowPlaying(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/metrics":
			_, _ = w.Write([]byte("hertz_sessions_active 2\n"))
		case "/v1/public/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"now_playing": map[string]string{
					"title":      "Song",
					"artist":     "Artist",
					"updated_at": "2026-11-07T15:00:00Z",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	port := testServerPort(t, engine.URL)

	app, _ := newTestApp(t, true)
	app.settings.HTTPPort = port
	app.settings.PublicHost = "radio.example.com"
	app.engine.running = true

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, adminRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantAudio := "http://radio.example.com:" + strconv.Itoa(port) + "/live.ogg"
	if got.AudioURL != wantAudio {
		t.Fatalf("audio_url=%q want %q", got.AudioURL, wantAudio)
	}
	wantMeta := "http://radio.example.com:" + strconv.Itoa(port) + "/v1/public/status"
	if got.MetadataURL != wantMeta {
		t.Fatalf("metadata_url=%q want %q", got.MetadataURL, wantMeta)
	}
	if got.NowPlaying == nil || got.NowPlaying.Title != "Song" || got.NowPlaying.Artist != "Artist" {
		t.Fatalf("now_playing=%+v", got.NowPlaying)
	}
}

func testServerPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestAdminNowPlayingForwardsPublisherToken(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	var gotTitle, gotArtist string
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nowplaying" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		signer, err := auth.NewSigner(secret, false)
		if err != nil {
			t.Errorf("signer: %v", err)
			http.Error(w, "signer", http.StatusInternalServerError)
			return
		}
		claims, err := signer.Verify(bearerForTest(r), auth.KindPublish, "main")
		if err != nil {
			http.Error(w, "token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		if claims.Subject != "admin" {
			http.Error(w, "subject", http.StatusForbidden)
			return
		}
		var body struct {
			Title  string `json:"title"`
			Artist string `json:"artist"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotTitle, gotArtist = body.Title, body.Artist
		w.WriteHeader(http.StatusOK)
	}))
	defer engine.Close()

	app, s := newTestApp(t, true)
	s.AuthSecret = secret
	app.settings.AuthSecret = secret
	app.settings.HTTPPort = testServerPort(t, engine.URL)
	app.engine.running = true

	body := strings.NewReader(`{"title":"Song","artist":"Artist"}`)
	req := adminRequest(http.MethodPost, "/api/nowplaying", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HERTZ-Admin", "1")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotTitle != "Song" || gotArtist != "Artist" {
		t.Fatalf("forwarded title=%q artist=%q", gotTitle, gotArtist)
	}
}

func bearerForTest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func TestAdminNowPlayingRefusesStoppedEngine(t *testing.T) {
	app, _ := newTestApp(t, true)
	req := adminRequest(http.MethodPost, "/api/nowplaying", strings.NewReader(`{"title":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HERTZ-Admin", "1")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMetricSum(t *testing.T) {
	metrics := `# HELP hertz_ingest_bitrate_bps bitrate
# TYPE hertz_ingest_bitrate_bps gauge
hertz_ingest_bitrate_bps{stream="main"} 133200
hertz_ingest_bitrate_bps{stream="other"} 64000
hertz_ingest_packets_total{stream="main"} 264
hertz_ingest_packets_total{stream="main2"} 999
hertz_ingest_packets_total_extra{stream="main"} 12345
hertz_sessions_active 3
`
	if got := metricSum(metrics, "hertz_ingest_bitrate_bps", "main"); got != 133200 {
		t.Fatalf("bitrate=%v", got)
	}
	if got := metricSum(metrics, "hertz_ingest_packets_total", "main"); got != 264 {
		t.Fatalf("packets=%v (matched a different metric or stream)", got)
	}
	if got := metricSum(metrics, "hertz_sessions_active", ""); got != 3 {
		t.Fatalf("sessions=%v", got)
	}
}

func TestAdoptedExternalEngineCannotBeStopped(t *testing.T) {
	s := validSettings()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := NewEngine(s, t.TempDir(), nil, "", logger)
	e.running = true
	e.owned = false
	if err := e.Stop(); !errors.Is(err, errEngineExternal) {
		t.Fatalf("Stop() error=%v, want errEngineExternal", err)
	}
	if err := e.Restart(); !errors.Is(err, errEngineExternal) {
		t.Fatalf("Restart() error=%v, want errEngineExternal", err)
	}
}

func TestParseSource(t *testing.T) {
	now := time.Now().Unix()
	metrics := `
hertz_ingest_bitrate_bps{stream="main"} 133200
hertz_ingest_packets_total{stream="main"} 264
hertz_ingest_last_packet_seconds{stream="main"} ` + strconv.FormatInt(now, 10) + `
`
	src := parseSource(metrics, "main")
	if !src.Online || src.BitrateBPS != 133200 || src.PacketsTotal != 264 {
		t.Fatalf("unexpected source stats: %+v", src)
	}
	old := parseSource(`
hertz_ingest_last_packet_seconds{stream="main"} `+strconv.FormatInt(now-30, 10)+`
`, "main")
	if old.Online {
		t.Fatal("stale source reported online")
	}
}
