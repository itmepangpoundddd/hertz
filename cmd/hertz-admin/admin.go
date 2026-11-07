package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"hertz/internal/auth"
)

//go:embed ui/*.html ui/*.css ui/*.js ui/*.png ui/*.ico
var uiFS embed.FS

//go:embed assets/*
var assetsFS embed.FS

// App is the HERTZ administrative application: an embedded HTTP UI plus a
// supervised engine child process.
type App struct {
	logger  *slog.Logger
	version string
	engine  *Engine

	settingsMu sync.RWMutex
	settings   Settings

	server    *http.Server
	listener  net.Listener
	setupDone chan struct{}
	setupOnce sync.Once
	startedAt time.Time
}

func embeddedEngine() []byte {
	raw, err := assetsFS.ReadFile("assets/hertz-engine.exe")
	if err != nil {
		return nil
	}
	return raw
}

func NewApp(s Settings, version string, logger *slog.Logger) *App {
	external := strings.TrimSpace(os.Getenv("HERTZ_ADMIN_ENGINE"))
	app := &App{
		logger:    logger,
		version:   version,
		settings:  s,
		setupDone: make(chan struct{}),
		startedAt: time.Now(),
	}
	app.engine = NewEngine(s, dataDir(), embeddedEngine(), external, logger)
	return app
}

func (a *App) CurrentSettings() Settings {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings
}

func (a *App) Engine() *Engine { return a.engine }

func (a *App) SetupDone() <-chan struct{} { return a.setupDone }

func (a *App) completeSetup() {
	a.setupOnce.Do(func() { close(a.setupDone) })
}

// StartServer binds the admin UI to loopback and starts serving. Binding before
// the tray starts means a port conflict is reported while a console is still
// available.
func (a *App) StartServer() error {
	ln, err := net.Listen("tcp", adminAddr())
	if err != nil {
		return fmt.Errorf("listen %s: %w", adminAddr(), err)
	}
	a.listener = ln
	a.server = &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		a.logger.Info("admin UI listening", "url", a.adminURL())
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("admin server failed", "err", err)
		}
	}()
	return nil
}

func (a *App) adminURL() string {
	host := adminAddr()
	if strings.HasPrefix(host, "0.0.0.0:") || strings.HasPrefix(host, ":") {
		_, port, _ := net.SplitHostPort(host)
		host = net.JoinHostPort("127.0.0.1", port)
	}
	return "http://" + host
}

func (a *App) Shutdown(ctx context.Context) {
	if a.server != nil {
		_ = a.server.Shutdown(ctx)
	}
	_ = a.engine.Stop()
	closeJobObject()
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /", a.handleIndex)
	mux.HandleFunc("GET /setup", a.handleSetupPage)
	mux.HandleFunc("GET /settings", a.handleSettingsPage)
	mux.HandleFunc("GET /assets/{name}", a.handleAsset)
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/share", a.handleShare)
	mux.HandleFunc("GET /api/logs", a.handleLogs)
	mux.HandleFunc("POST /api/setup", a.requireAdmin(a.handleSaveSetup))
	mux.HandleFunc("POST /api/settings", a.requireAdmin(a.handleSaveSettings))
	mux.HandleFunc("POST /api/nowplaying", a.requireAdmin(a.handleNowPlaying))
	mux.HandleFunc("POST /api/engine", a.requireAdmin(a.handleEngine))
	mux.HandleFunc("POST /api/open", a.requireAdmin(a.handleOpen))
	return a.localOnly(a.securityHeaders(mux))
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self' 'unsafe-inline'; connect-src 'self'; base-uri 'none'; "+
				"form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// localOnly defends the loopback-bound admin UI against DNS rebinding: both the
// TCP peer and the Host header must be loopback, regardless of the resolved
// address.
func (a *App) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !remoteIsLoopback(r.RemoteAddr) {
			httpError(w, http.StatusForbidden, "admin UI is loopback-only")
			return
		}
		if !hostIsLoopback(r.Host) {
			httpError(w, http.StatusForbidden, "invalid Host header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-HERTZ-Admin") != "1" {
			httpError(w, http.StatusForbidden, "missing X-HERTZ-Admin header")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !hostIsLoopback(u.Host) {
				httpError(w, http.StatusForbidden, "cross-origin admin request rejected")
				return
			}
		}
		next(w, r)
	}
}

func remoteIsLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func hostIsLoopback(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	s := a.CurrentSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"app":            "hertz-admin",
		"version":        a.version,
		"setup_complete": s.SetupComplete,
	})
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !a.CurrentSettings().SetupComplete {
		http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
		return
	}
	a.servePage(w, "dashboard.html")
}

func (a *App) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if a.CurrentSettings().SetupComplete {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	a.servePage(w, "setup.html")
}

func (a *App) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if !a.CurrentSettings().SetupComplete {
		http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
		return
	}
	// setup.html doubles as the settings form; its JavaScript switches labels
	// and the submit endpoint based on the URL path.
	a.servePage(w, "setup.html")
}

func (a *App) servePage(w http.ResponseWriter, name string) {
	raw, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		http.Error(w, "UI asset missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(raw)
}

func (a *App) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	allowed := map[string]string{
		"style.css": "text/css; charset=utf-8",
		"app.js":    "text/javascript; charset=utf-8",
		"icon.png":  "image/png",
		"icon.ico":  "image/x-icon",
	}
	ct, ok := allowed[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := fs.ReadFile(uiFS, "ui/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	// The admin UI ships inside the exe. Do not let a browser keep an old
	// app.js/style.css after an upgrade, or translated/new UI stays hidden.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

type settingsView struct {
	SetupComplete bool `json:"setup_complete"`

	StreamID    string `json:"stream_id"`
	HTTPPort    int    `json:"http_port"`
	MetricsPort int    `json:"metrics_port"`
	IngestPort  int    `json:"ingest_port"`
	ICEPort     int    `json:"ice_port"`

	PublicHost string `json:"public_host"`
	PublicIP   string `json:"public_ip"`

	IngestAllowed string `json:"ingest_allowed"`
	IncludeLoop   bool   `json:"include_loopback"`
	MetricsPublic bool   `json:"metrics_public"`

	MaxSessions      int `json:"max_sessions"`
	MaxSessionsPerIP int `json:"max_sessions_per_ip"`

	TurnURLs   string `json:"turn_urls"`
	TurnSecret bool   `json:"turn_secret_set"`
	TurnTTLHrs int    `json:"turn_ttl_hours"`

	LinkTTLHours int  `json:"link_ttl_hours"`
	AuthSecret   bool `json:"auth_secret_set"`
}

func viewOf(s Settings) settingsView {
	return settingsView{
		SetupComplete:    s.SetupComplete,
		StreamID:         s.StreamID,
		HTTPPort:         s.HTTPPort,
		MetricsPort:      s.MetricsPort,
		IngestPort:       s.IngestPort,
		ICEPort:          s.ICEPort,
		PublicHost:       s.PublicHost,
		PublicIP:         s.PublicIP,
		IngestAllowed:    s.IngestAllowed,
		IncludeLoop:      s.IncludeLoop,
		MetricsPublic:    s.MetricsPublic,
		MaxSessions:      s.MaxSessions,
		MaxSessionsPerIP: s.MaxSessionsPerIP,
		TurnURLs:         s.TurnURLs,
		TurnSecret:       s.TurnSecret != "",
		TurnTTLHrs:       s.TurnTTLHrs,
		LinkTTLHours:     s.LinkTTLHours,
		AuthSecret:       s.AuthSecret != "",
	}
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	s := a.CurrentSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_complete": s.SetupComplete,
		"settings":       viewOf(s),
		"detected_ip":    localIPv4(),
		"detected_cidr":  localSubnetCIDR(),
		"data_dir":       dataDir(),
		"config_path":    settingsPath(),
		"admin_url":      a.adminURL(),
		"version":        a.version,
		"engine_log":     a.engine.logPath(),
	})
}

type settingsInput struct {
	Settings
	RotateSecret bool `json:"rotate_secret"`
}

func (a *App) handleSaveSetup(w http.ResponseWriter, r *http.Request) {
	if a.CurrentSettings().SetupComplete {
		httpError(w, http.StatusConflict, "setup is already complete")
		return
	}
	var in settingsInput
	if err := decodeJSON(w, r, &in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	next := in.Settings
	next.SetupComplete = true
	next.AuthSecret = a.CurrentSettings().AuthSecret
	warning, err := a.applySettings(next, true)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.completeSetup()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"warning":  warning,
		"redirect": "/",
	})
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	prev := a.CurrentSettings()
	if !prev.SetupComplete {
		httpError(w, http.StatusConflict, "setup is not complete")
		return
	}
	var in settingsInput
	if err := decodeJSON(w, r, &in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	next := in.Settings
	next.SetupComplete = true
	next.AuthSecret = prev.AuthSecret
	if in.RotateSecret {
		next.AuthSecret = randomSecret()
	}
	warning, err := a.applySettings(next, false)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"warning":  warning,
		"redirect": "/",
	})
}

// applySettings validates, persists and applies new settings. The engine is
// started or restarted synchronously so the UI can show a real error instead of
// silently failing in the background.
func (a *App) applySettings(next Settings, firstRun bool) (string, error) {
	prev := a.CurrentSettings()
	next = next.normalized()
	next.SetupComplete = true
	if next.TurnURLs == "" {
		next.TurnSecret = ""
	} else if next.TurnSecret == "" {
		next.TurnSecret = prev.TurnSecret
	}
	if err := next.validate(); err != nil {
		return "", err
	}

	snap := a.engine.Snapshot()
	running := snap.Running
	if snap.Running && !snap.Owned {
		// Do not pretend we can reconfigure a process started elsewhere.
		if err := saveSettings(next); err != nil {
			return "", fmt.Errorf("could not save settings: %w", err)
		}
		a.settingsMu.Lock()
		a.settings = next
		a.settingsMu.Unlock()
		a.engine.SetSettings(next)
		return "HERTZ is already running outside this app. The new settings take effect after that process restarts.", nil
	}
	if err := checkPorts(next, prev, running && !firstRun); err != nil {
		return "", err
	}
	if err := saveSettings(next); err != nil {
		return "", fmt.Errorf("could not save settings: %w", err)
	}

	a.settingsMu.Lock()
	a.settings = next
	a.settingsMu.Unlock()
	a.engine.SetSettings(next)

	var engineErr error
	if running {
		engineErr = a.engine.Restart()
	} else {
		engineErr = a.engine.Start()
	}
	if engineErr != nil {
		a.logger.Error("engine did not start after settings change", "err", engineErr)
		return engineErr.Error(), nil
	}
	return "", nil
}

func checkPorts(next, prev Settings, skipUnchanged bool) error {
	type check struct {
		name    string
		network string
		next    int
		prev    int
	}
	checks := []check{
		{"HTTP", "tcp4", next.HTTPPort, prev.HTTPPort},
		{"metrics", "tcp4", next.MetricsPort, prev.MetricsPort},
		{"ingest", "udp4", next.IngestPort, prev.IngestPort},
		{"ICE", "udp4", next.ICEPort, prev.ICEPort},
	}
	for _, c := range checks {
		if skipUnchanged && c.next == c.prev {
			continue
		}
		if c.next == adminPort() {
			continue // admin port was already rejected by validate
		}
		if err := portAvailable(c.network, c.next); err != nil {
			return fmt.Errorf("port %s %d is already in use; choose another port", c.name, c.next)
		}
	}
	return nil
}

func portAvailable(network string, port int) error {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	lc := exclusiveListenConfig()
	if strings.HasPrefix(network, "tcp") {
		ln, err := lc.Listen(context.Background(), network, addr)
		if err != nil {
			return err
		}
		return ln.Close()
	}
	conn, err := lc.ListenPacket(context.Background(), network, addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

type engineView struct {
	Running   bool   `json:"running"`
	Healthy   bool   `json:"healthy"`
	Managed   bool   `json:"managed"`
	Starting  bool   `json:"starting"`
	LastError string `json:"last_error,omitempty"`
	LogPath   string `json:"log_path"`

	HTTPPort    int `json:"http_port"`
	MetricsPort int `json:"metrics_port"`
	IngestPort  int `json:"ingest_port"`
	ICEPort     int `json:"ice_port"`
}

type sourceView struct {
	Online       bool    `json:"online"`
	BitrateBPS   float64 `json:"bitrate_bps"`
	PacketsTotal float64 `json:"packets_total"`
	LastSeenUnix float64 `json:"last_seen_unix"`
	LastSeenAge  float64 `json:"last_seen_age_seconds"`
}

type nowPlayingView struct {
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	UpdatedAt string `json:"updated_at"`
}

type statusResponse struct {
	SetupComplete bool   `json:"setup_complete"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`

	Engine        engineView `json:"engine"`
	Source        sourceView `json:"source"`
	Listeners     float64    `json:"listeners"`
	Sessions      float64    `json:"sessions_active"`
	Dropped       float64    `json:"dropped_packets"`
	TargetBitrate float64    `json:"target_bitrate_bps"`

	Publisher    map[string]any  `json:"publisher"`
	ListenerBase string          `json:"listener_base"`
	AudioURL     string          `json:"audio_url"`
	MetadataURL  string          `json:"metadata_url"`
	NowPlaying   *nowPlayingView `json:"now_playing,omitempty"`
	Settings     settingsView    `json:"settings"`
	Warnings     []string        `json:"warnings"`
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildStatus())
}

func (a *App) buildStatus() statusResponse {
	s := a.CurrentSettings()
	snap := a.engine.Snapshot()

	healthy := false
	if snap.Running && !snap.Starting {
		healthy = a.engine.Health()
	}
	ev := engineView{
		Running:     snap.Running,
		Healthy:     healthy,
		Managed:     snap.Owned,
		Starting:    snap.Starting,
		LastError:   snap.LastError,
		LogPath:     snap.LogPath,
		HTTPPort:    s.HTTPPort,
		MetricsPort: s.MetricsPort,
		IngestPort:  s.IngestPort,
		ICEPort:     s.ICEPort,
	}

	var metrics string
	if healthy {
		metrics, _ = fetchMetrics(s.MetricsPort)
	}
	source := parseSource(metrics, s.StreamID)
	resp := statusResponse{
		SetupComplete: s.SetupComplete,
		Version:       a.version,
		UptimeSeconds: int64(time.Since(a.startedAt).Seconds()),
		Engine:        ev,
		Source:        source,
		Listeners:     metricSum(metrics, "hertz_listeners", s.StreamID),
		Sessions:      metricSum(metrics, "hertz_sessions_active", ""),
		Dropped:       metricSum(metrics, "hertz_fanout_dropped_packets_total", s.StreamID),
		TargetBitrate: metricSum(metrics, "hertz_source_target_bitrate_bps", s.StreamID),
		Publisher: map[string]any{
			"host":      s.PublicHost,
			"port":      s.IngestPort,
			"stream_id": s.StreamID,
			"address":   s.publisherAddress(),
		},
		ListenerBase: s.listenerBaseURL(),
		AudioURL:     s.audioURL(),
		MetadataURL:  s.metadataURL(),
		Settings:     viewOf(s),
	}

	if healthy {
		if pub, err := fetchPublicStatus(s.HTTPPort); err == nil {
			resp.NowPlaying = pub.NowPlaying
		}
	}

	if isLoopbackHost(s.PublicHost) {
		resp.Warnings = append(resp.Warnings,
			"The listener address is localhost — shared links will open on this machine only. Set a LAN IP or public domain in Settings.")
	}
	if !snap.Running {
		resp.Warnings = append(resp.Warnings, "HERTZ is not running. Press Start engine.")
	} else if !healthy {
		resp.Warnings = append(resp.Warnings, "HERTZ is running but slow to respond; checking again.")
	}
	if snap.LastError != "" {
		resp.Warnings = append(resp.Warnings, "Last error: "+snap.LastError)
	}
	if healthy && !source.Online {
		resp.Warnings = append(resp.Warnings, "No audio received from the source — start butt or your mixer and connect it to the UDP ingest port.")
	}
	if s.IngestAllowed == "" {
		resp.Warnings = append(resp.Warnings, "RTP is currently accepted from every IP — restrict the source CIDR in Settings.")
	}
	return resp
}

func fetchMetrics(port int) (string, error) {
	client := http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metrics status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type enginePublicStatus struct {
	NowPlaying *nowPlayingView `json:"now_playing"`
}

func fetchPublicStatus(port int) (enginePublicStatus, error) {
	client := http.Client{Timeout: 900 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/public/status", port))
	if err != nil {
		return enginePublicStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return enginePublicStatus{}, fmt.Errorf("public status %d", resp.StatusCode)
	}
	var out enginePublicStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return enginePublicStatus{}, err
	}
	return out, nil
}

// handleNowPlaying signs a short-lived publisher token and forwards the title
// to the embedded engine. The token never reaches the browser.
func (a *App) handleNowPlaying(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.engine.Snapshot().Running {
		httpError(w, http.StatusServiceUnavailable, "HERTZ engine is not running")
		return
	}
	s := a.CurrentSettings()
	signer, err := auth.NewSigner(s.AuthSecret, false)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "auth is not configured")
		return
	}
	now := time.Now()
	token, err := signer.Sign(auth.Claims{
		Subject:   "admin",
		Stream:    s.StreamID,
		Kind:      auth.KindPublish,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload, err := json.Marshal(map[string]string{
		"title":  strings.TrimSpace(req.Title),
		"artist": strings.TrimSpace(req.Artist),
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	target := fmt.Sprintf("http://127.0.0.1:%d/v1/nowplaying", s.HTTPPort)
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Header.Set("Authorization", "Bearer "+token)
	out.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(out)
	if err != nil {
		httpError(w, http.StatusBadGateway, "could not reach HERTZ engine: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		httpError(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func parseSource(metrics, stream string) sourceView {
	var s sourceView
	if metrics == "" {
		return s
	}
	s.BitrateBPS = metricSum(metrics, "hertz_ingest_bitrate_bps", stream)
	s.PacketsTotal = metricSum(metrics, "hertz_ingest_packets_total", stream)
	s.LastSeenUnix = metricSum(metrics, "hertz_ingest_last_packet_seconds", stream)
	if s.LastSeenUnix > 0 {
		s.LastSeenAge = float64(time.Now().Unix()) - s.LastSeenUnix
		if s.LastSeenAge < 0 {
			s.LastSeenAge = 0
		}
		s.Online = s.LastSeenAge <= 8
	}
	return s
}

// metricSum adds every sample of a metric, optionally filtered by the stream
// label. It deliberately avoids a Prometheus dependency: the admin only needs
// a handful of gauges and counters.
func metricSum(text, name, stream string) float64 {
	var total float64
	label := ""
	if stream != "" {
		label = `stream="` + stream + `"`
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, name) {
			continue
		}
		rest := line[len(name):]
		if rest != "" && rest[0] != '{' && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		if label != "" && !strings.Contains(rest, label) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			total += v
		}
	}
	return total
}

type shareInfo struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	TTLHours  int       `json:"ttl_hours"`
	ExpiresAt time.Time `json:"-"`
}

// newShare mints a short-lived listener token. The token lives in the URL
// fragment so it never appears in HERTZ access logs or Referer headers.
func (a *App) newShare(ttlHours int) (shareInfo, error) {
	s := a.CurrentSettings()
	if ttlHours < 1 || ttlHours > 720 {
		ttlHours = s.LinkTTLHours
	}
	signer, err := auth.NewSigner(s.AuthSecret, false)
	if err != nil {
		return shareInfo{}, fmt.Errorf("auth secret is invalid: %w", err)
	}
	expires := time.Now().Add(time.Duration(ttlHours) * time.Hour)
	token, err := signer.Sign(auth.Claims{
		Subject:   "listener-" + auth.NewID()[:8],
		Stream:    s.StreamID,
		Kind:      auth.KindSubscribe,
		ExpiresAt: expires.Unix(),
	})
	if err != nil {
		return shareInfo{}, err
	}
	return shareInfo{
		URL:       s.listenerBaseURL() + "#t=" + token,
		Token:     token,
		TTLHours:  ttlHours,
		ExpiresAt: expires,
	}, nil
}

func (a *App) handleShare(w http.ResponseWriter, r *http.Request) {
	ttlHours := a.CurrentSettings().LinkTTLHours
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 720 {
			ttlHours = n
		}
	}
	share, err := a.newShare(ttlHours)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	png, err := qrcode.Encode(share.URL, qrcode.Medium, 320)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        share.URL,
		"token":      share.Token,
		"ttl_hours":  share.TTLHours,
		"expires_at": share.ExpiresAt.Format(time.RFC3339),
		"qr":         "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	lines := 120
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 20 {
				n = 20
			}
			if n > 500 {
				n = 500
			}
			lines = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":  a.engine.logPath(),
		"lines": a.engine.TailLog(lines),
	})
}

func (a *App) handleEngine(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	switch in.Action {
	case "start":
		err = a.engine.Start()
	case "stop":
		err = a.engine.Stop()
	case "restart":
		err = a.engine.Restart()
	default:
		httpError(w, http.StatusBadRequest, "action must be start, stop or restart")
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errEngineExternal) {
			status = http.StatusConflict
		}
		httpError(w, status, friendlyEngineError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": a.engine.Snapshot().Running})
}

func (a *App) handleOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	switch in.Target {
	case "dashboard":
		err = openBrowser(a.adminURL() + "/")
	case "settings":
		err = openBrowser(a.adminURL() + "/settings")
	case "logs":
		dir := filepath.Dir(a.engine.logPath())
		err = os.MkdirAll(dir, 0o700)
		if err == nil {
			err = openFolder(dir)
		}
	default:
		httpError(w, http.StatusBadRequest, "unknown target")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func friendlyEngineError(err error) string {
	switch {
	case errors.Is(err, errEngineExternal):
		return "HERTZ was started by another program, so it cannot be stopped from this page."
	case strings.Contains(err.Error(), "did not answer /healthz"):
		return "HERTZ did not respond within 15 seconds. Check the log and active ports."
	case strings.Contains(err.Error(), "exited during startup"):
		return "HERTZ exited during startup. Check the log below."
	default:
		return err.Error()
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
