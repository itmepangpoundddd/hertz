// Package config loads HERTZ configuration from environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the complete runtime configuration for one HERTZ node.
type Config struct {
	// HTTP / control plane.
	HTTPAddr         string
	MetricsAddr      string
	AllowedOrigins   []string
	TrustProxy       bool
	TrustedProxyHops int
	EnablePProf      bool
	ReadHeaderLimit  time.Duration
	SessionTTL       time.Duration

	// Media ingest.
	StreamID          string
	IngestAddr        string
	IngestUnix        string
	IngestPayloadType uint8
	IngestSSRC        uint32
	IngestAllowed     []*net.IPNet
	IngestMaxPPS      int
	JitterWindow      time.Duration
	JitterTick        time.Duration
	MaxPacketSize     int
	UDPReadBuffer     int

	// WebRTC / ICE.
	ICEUDPPort        int
	IncludeLoop       bool
	PublicIP          string
	ICEServers        []ICEServer // server-side ICE agent
	BrowserICEServers []ICEServer // static listener ICE servers; do not put static TURN secrets here
	TURNRESTSecret    string
	TURNRESTURLs      []string
	TURNCredentialTTL time.Duration
	InitialBitrate    int
	MinBitrate        int
	MaxBitrate        int
	PacerMode         string // "noop" (low latency) or "leaky" (shaping; unbounded queue)

	// Fanout.
	QueueSize     int
	StatsInterval time.Duration
	SharedPayload bool // experimental, off by default

	// Auth.
	AuthSecret       string
	AllowInsecureDev bool

	// Limits.
	MaxSessions         int
	MaxSessionsPerIP    int
	MaxHTTPListeners    int
	MaxSDPBytes         int64
	MaxMediaSections    int
	JoinRatePerSecond   float64
	JoinBurst           int
	IPJoinRatePerSecond float64
	IPJoinBurst         int

	// Optional control hook for source-side adaptive bitrate.
	EncoderControlURL   string
	EncoderControlToken string

	// Logging.
	LogLevel slog.Level
}

// ICEServer mirrors the subset of webrtc.ICEServer that is safe to expose to browsers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Load reads environment variables and applies defaults.
func Load() (Config, error) {
	ingestPT := envInt("HERTZ_INGEST_PT", 111)
	ingestSSRC := envInt("HERTZ_INGEST_SSRC", 0)
	cfg := Config{
		HTTPAddr:            env("HERTZ_HTTP_ADDR", ":8080"),
		MetricsAddr:         env("HERTZ_METRICS_ADDR", ":9090"),
		AllowedOrigins:      splitCSV(env("HERTZ_ALLOWED_ORIGINS", "*")),
		TrustProxy:          envBool("HERTZ_TRUST_PROXY", false),
		TrustedProxyHops:    envInt("HERTZ_TRUSTED_PROXY_HOPS", 1),
		EnablePProf:         envBool("HERTZ_PPROF", false),
		ReadHeaderLimit:     5 * time.Second,
		SessionTTL:          envDuration("HERTZ_SESSION_TTL", 12*time.Hour),
		StreamID:            env("HERTZ_STREAM_ID", "main"),
		IngestAddr:          env("HERTZ_INGEST_ADDR", ":5004"),
		IngestUnix:          env("HERTZ_INGEST_UNIX", ""),
		IngestPayloadType:   uint8(ingestPT),
		IngestSSRC:          uint32(ingestSSRC),
		IngestMaxPPS:        envInt("HERTZ_INGEST_MAX_PPS", 500),
		JitterWindow:        envDuration("HERTZ_JITTER_WINDOW", 40*time.Millisecond),
		JitterTick:          envDuration("HERTZ_JITTER_TICK", 5*time.Millisecond),
		MaxPacketSize:       envInt("HERTZ_MAX_PACKET_SIZE", 1500),
		UDPReadBuffer:       envInt("HERTZ_UDP_READ_BUFFER", 4<<20),
		ICEUDPPort:          envInt("HERTZ_ICE_UDP_PORT", 8443),
		IncludeLoop:         envBool("HERTZ_ICE_LOOPBACK", false),
		PublicIP:            env("HERTZ_PUBLIC_IP", ""),
		TURNRESTSecret:      env("HERTZ_TURN_REST_SECRET", ""),
		TURNRESTURLs:        splitCSV(env("HERTZ_TURN_URLS", "")),
		TURNCredentialTTL:   envDuration("HERTZ_TURN_TTL", time.Hour),
		InitialBitrate:      envInt("HERTZ_INITIAL_BITRATE", 256_000),
		MinBitrate:          envInt("HERTZ_MIN_BITRATE", 96_000),
		MaxBitrate:          envInt("HERTZ_MAX_BITRATE", 512_000),
		PacerMode:           env("HERTZ_PACER", "noop"),
		QueueSize:           envInt("HERTZ_QUEUE_SIZE", 10),
		StatsInterval:       envDuration("HERTZ_STATS_INTERVAL", 5*time.Second),
		SharedPayload:       envBool("HERTZ_SHARED_PAYLOAD", false),
		AuthSecret:          env("HERTZ_AUTH_SECRET", ""),
		AllowInsecureDev:    envBool("HERTZ_ALLOW_INSECURE_DEV", false),
		MaxSessions:         envInt("HERTZ_MAX_SESSIONS", 5000),
		MaxSessionsPerIP:    envInt("HERTZ_MAX_SESSIONS_PER_IP", 8),
		MaxHTTPListeners:    envInt("HERTZ_MAX_HTTP_LISTENERS", 200),
		MaxSDPBytes:         int64(envInt("HERTZ_MAX_SDP_BYTES", 256<<10)),
		MaxMediaSections:    envInt("HERTZ_MAX_MEDIA_SECTIONS", 4),
		JoinRatePerSecond:   float64(envInt("HERTZ_JOIN_RATE_PER_SECOND", 50)),
		JoinBurst:           envInt("HERTZ_JOIN_BURST", 100),
		IPJoinRatePerSecond: float64(envInt("HERTZ_IP_JOIN_RATE_PER_SECOND", 5)),
		IPJoinBurst:         envInt("HERTZ_IP_JOIN_BURST", 10),
		EncoderControlURL:   env("HERTZ_ENCODER_CONTROL_URL", ""),
		EncoderControlToken: env("HERTZ_ENCODER_CONTROL_TOKEN", ""),
		LogLevel:            parseLevel(env("HERTZ_LOG_LEVEL", "info")),
	}

	if raw := env("HERTZ_ICE_SERVERS", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.ICEServers); err != nil {
			return Config{}, fmt.Errorf("HERTZ_ICE_SERVERS: %w", err)
		}
	}
	if raw := env("HERTZ_BROWSER_ICE_SERVERS", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.BrowserICEServers); err != nil {
			return Config{}, fmt.Errorf("HERTZ_BROWSER_ICE_SERVERS: %w", err)
		}
	}

	if raw := env("HERTZ_INGEST_ALLOWED_CIDRS", ""); raw != "" {
		for _, s := range splitCSV(raw) {
			_, n, err := net.ParseCIDR(s)
			if err != nil {
				return Config{}, fmt.Errorf("HERTZ_INGEST_ALLOWED_CIDRS %q: %w", s, err)
			}
			cfg.IngestAllowed = append(cfg.IngestAllowed, n)
		}
	}

	if ingestPT < 0 || ingestPT > 255 {
		return Config{}, fmt.Errorf("HERTZ_INGEST_PT must be 0-255")
	}
	if ingestSSRC < 0 || uint64(ingestSSRC) > uint64(^uint32(0)) {
		return Config{}, fmt.Errorf("HERTZ_INGEST_SSRC must be an unsigned 32-bit value")
	}
	if cfg.AuthSecret == "" && !cfg.AllowInsecureDev {
		return Config{}, fmt.Errorf("HERTZ_AUTH_SECRET is required unless HERTZ_ALLOW_INSECURE_DEV=true")
	}
	if cfg.MaxSessions <= 0 {
		return Config{}, fmt.Errorf("HERTZ_MAX_SESSIONS must be > 0")
	}
	if cfg.JitterWindow < 0 || cfg.JitterWindow > 500*time.Millisecond {
		return Config{}, fmt.Errorf("HERTZ_JITTER_WINDOW must be between 0 and 500ms")
	}
	if cfg.MinBitrate <= 0 || cfg.InitialBitrate < cfg.MinBitrate || cfg.MaxBitrate < cfg.InitialBitrate {
		return Config{}, fmt.Errorf("invalid bitrate bounds: min=%d initial=%d max=%d",
			cfg.MinBitrate, cfg.InitialBitrate, cfg.MaxBitrate)
	}
	if cfg.PacerMode != "noop" && cfg.PacerMode != "leaky" {
		return Config{}, fmt.Errorf("HERTZ_PACER must be noop or leaky")
	}
	if cfg.QueueSize < 4 {
		return Config{}, fmt.Errorf("HERTZ_QUEUE_SIZE must be >= 4")
	}
	if cfg.ICEUDPPort < 0 || cfg.ICEUDPPort > 65535 {
		return Config{}, fmt.Errorf("HERTZ_ICE_UDP_PORT must be 0-65535")
	}
	if cfg.JoinRatePerSecond <= 0 {
		return Config{}, fmt.Errorf("HERTZ_JOIN_RATE_PER_SECOND must be > 0")
	}
	if cfg.IPJoinRatePerSecond <= 0 || cfg.IPJoinBurst <= 0 {
		return Config{}, fmt.Errorf("HERTZ_IP_JOIN_RATE_PER_SECOND and HERTZ_IP_JOIN_BURST must be > 0")
	}
	if cfg.TrustProxy && cfg.TrustedProxyHops < 1 {
		return Config{}, fmt.Errorf("HERTZ_TRUSTED_PROXY_HOPS must be >= 1 when HERTZ_TRUST_PROXY=true")
	}
	if cfg.StatsInterval <= 0 {
		return Config{}, fmt.Errorf("HERTZ_STATS_INTERVAL must be > 0")
	}
	if cfg.PublicIP != "" && net.ParseIP(cfg.PublicIP) == nil {
		return Config{}, fmt.Errorf("HERTZ_PUBLIC_IP %q is not a valid IP", cfg.PublicIP)
	}
	if cfg.TURNRESTSecret != "" {
		if len(cfg.TURNRESTSecret) < 16 {
			return Config{}, fmt.Errorf("HERTZ_TURN_REST_SECRET must be at least 16 bytes")
		}
		if len(cfg.TURNRESTURLs) == 0 {
			return Config{}, fmt.Errorf("HERTZ_TURN_URLS is required when HERTZ_TURN_REST_SECRET is set")
		}
		if cfg.TURNCredentialTTL <= 0 {
			return Config{}, fmt.Errorf("HERTZ_TURN_TTL must be > 0")
		}
	}
	for _, srv := range cfg.BrowserICEServers {
		for _, u := range srv.URLs {
			if (strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:")) &&
				(srv.Username != "" || srv.Credential != "") {
				return Config{}, fmt.Errorf("HERTZ_BROWSER_ICE_SERVERS must not contain static TURN credentials; use HERTZ_TURN_REST_SECRET")
			}
		}
	}
	return cfg, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("%s must be an integer: %v", key, err))
	}
	return n
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("%s must be a boolean: %v", key, err))
	}
	return b
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("%s must be a duration: %v", key, err))
	}
	return d
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
