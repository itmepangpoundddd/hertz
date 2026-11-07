package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const adminAddrDefault = "127.0.0.1:8090"

var streamIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Settings is what the admin UI edits. It is stored as config.json in
// %LOCALAPPDATA%\HERTZ and translated into HERTZ_* environment variables for
// the engine child process.
type Settings struct {
	SetupComplete bool `json:"setup_complete"`

	StreamID    string `json:"stream_id"`
	HTTPPort    int    `json:"http_port"`
	MetricsPort int    `json:"metrics_port"`
	IngestPort  int    `json:"ingest_port"`
	ICEPort     int    `json:"ice_port"`

	PublicHost string `json:"public_host"` // host/IP used in listener links
	PublicIP   string `json:"public_ip"`   // optional NAT 1:1 public IP

	IngestAllowed string `json:"ingest_allowed"` // comma-separated CIDRs; empty = any
	IncludeLoop   bool   `json:"include_loopback"`
	MetricsPublic bool   `json:"metrics_public"` // false = bind metrics to loopback only

	MaxSessions      int `json:"max_sessions"`
	MaxSessionsPerIP int `json:"max_sessions_per_ip"`

	TurnURLs   string `json:"turn_urls"`
	TurnSecret string `json:"turn_secret"`
	TurnTTLHrs int    `json:"turn_ttl_hours"`

	LinkTTLHours int `json:"link_ttl_hours"`

	AuthSecret string `json:"auth_secret"`
}

func defaultSettings() Settings {
	ingest := "127.0.0.1/32"
	if lan := localSubnetCIDR(); lan != "" {
		ingest += "," + lan
	}
	return Settings{
		StreamID:         "main",
		HTTPPort:         8080,
		MetricsPort:      9090,
		IngestPort:       5004,
		ICEPort:          8443,
		PublicHost:       localIPv4(),
		IngestAllowed:    ingest,
		IncludeLoop:      true,
		MetricsPublic:    false,
		MaxSessions:      5000,
		MaxSessionsPerIP: 8,
		TurnTTLHrs:       1,
		LinkTTLHours:     24,
		AuthSecret:       randomSecret(),
	}
}

func dataDir() string {
	if dir := os.Getenv("HERTZ_ADMIN_DATA"); dir != "" {
		return dir
	}
	// Prefer LOCALAPPDATA on Windows. os.UserConfigDir also points at
	// %APPDATA%, but %LOCALAPPDATA% is the conventional place for per-machine
	// application state that must not roam.
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "HERTZ")
		}
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "HERTZ")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hertz")
}

func settingsPath() string { return filepath.Join(dataDir(), "config.json") }

func loadSettings() (Settings, error) {
	path := settingsPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultSettings().normalized(), nil
		}
		return Settings{}, err
	}
	s := defaultSettings()
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s.normalized(), nil
}

func saveSettings(s Settings) error {
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath())
}

// normalized returns a copy with whitespace trimmed and empty optional fields
// filled with sensible defaults. It has a value receiver on purpose: validate
// must not appear to mutate its receiver.
func (s Settings) normalized() Settings {
	s.StreamID = strings.TrimSpace(s.StreamID)
	if s.StreamID == "" {
		s.StreamID = "main"
	}
	s.PublicHost = strings.Trim(strings.TrimSpace(s.PublicHost), "[]")
	if s.PublicHost == "" {
		s.PublicHost = localIPv4()
	}
	s.PublicIP = strings.TrimSpace(s.PublicIP)
	s.IngestAllowed = normalizeCIDRList(s.IngestAllowed)
	s.TurnURLs = strings.TrimSpace(s.TurnURLs)
	s.TurnSecret = strings.TrimSpace(s.TurnSecret)
	if s.AuthSecret == "" {
		s.AuthSecret = randomSecret()
	}
	if s.LinkTTLHours <= 0 {
		s.LinkTTLHours = 24
	}
	if s.TurnTTLHrs <= 0 {
		s.TurnTTLHrs = 1
	}
	return s
}

func normalizeCIDRList(raw string) string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

func (s Settings) validate() error {
	if !streamIDPattern.MatchString(s.StreamID) {
		return errors.New("Stream ID must contain only letters, numbers, dots, dashes, or underscores (1-64 characters)")
	}
	ports := map[string]int{
		"HTTP": s.HTTPPort, "metrics": s.MetricsPort,
		"ingest": s.IngestPort, "ICE": s.ICEPort,
	}
	for name, p := range ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("port %s must be between 1-65535", name)
		}
	}
	if s.HTTPPort == s.MetricsPort || s.HTTPPort == s.IngestPort ||
		s.HTTPPort == s.ICEPort || s.MetricsPort == s.IngestPort ||
		s.MetricsPort == s.ICEPort || s.IngestPort == s.ICEPort {
		return errors.New("HTTP, metrics, ingest, and ICE ports must be unique")
	}
	for name, p := range ports {
		if p == adminPort() {
			return fmt.Errorf("port %s conflicts with the admin page (%d)", name, adminPort())
		}
	}
	if s.MaxSessions < 1 {
		return errors.New("maximum listeners must be greater than 0")
	}
	if s.MaxSessionsPerIP < 0 {
		return errors.New("listeners per IP must not be negative")
	}
	if s.LinkTTLHours < 1 || s.LinkTTLHours > 720 {
		return errors.New("link lifetime must be between 1-720 hours")
	}
	if len(s.AuthSecret) < 32 {
		return errors.New("auth secret is too short")
	}
	if s.PublicIP != "" && net.ParseIP(s.PublicIP) == nil {
		return fmt.Errorf("Public IP %q is not a valid IP address", s.PublicIP)
	}
	if strings.ContainsAny(s.PublicHost, " /\\") || strings.Contains(s.PublicHost, "://") {
		return errors.New("listener address must be a hostname or IP only (do not include http://)")
	}
	if strings.Contains(s.PublicHost, ":") && net.ParseIP(s.PublicHost) == nil {
		return errors.New("listener address must not include a port (there is a separate port field)")
	}
	for _, cidr := range strings.Split(s.IngestAllowed, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("source CIDR %q is not valid", cidr)
		}
	}
	if s.TurnSecret != "" {
		if len(s.TurnSecret) < 16 {
			return errors.New("TURN secret must be at least 16 characters")
		}
		if s.TurnURLs == "" {
			return errors.New("TURN URL is required when TURN is enabled")
		}
		for _, raw := range strings.Split(s.TurnURLs, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "turn" && u.Scheme != "turns") {
				return fmt.Errorf("TURN URL %q must start with turn: or turns:", raw)
			}
		}
		if s.TurnTTLHrs < 1 || s.TurnTTLHrs > 24 {
			return errors.New("TURN credential lifetime must be between 1-24 hours")
		}
	}
	return nil
}

func randomSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s Settings) engineEnv() []string {
	metricsAddr := "127.0.0.1:" + strconv.Itoa(s.MetricsPort)
	if s.MetricsPublic {
		metricsAddr = ":" + strconv.Itoa(s.MetricsPort)
	}
	env := []string{
		"HERTZ_HTTP_ADDR=:" + strconv.Itoa(s.HTTPPort),
		"HERTZ_METRICS_ADDR=" + metricsAddr,
		"HERTZ_INGEST_ADDR=:" + strconv.Itoa(s.IngestPort),
		"HERTZ_ICE_UDP_PORT=" + strconv.Itoa(s.ICEPort),
		"HERTZ_STREAM_ID=" + s.StreamID,
		"HERTZ_AUTH_SECRET=" + s.AuthSecret,
		"HERTZ_ALLOW_INSECURE_DEV=false",
		"HERTZ_ICE_LOOPBACK=" + strconv.FormatBool(s.IncludeLoop),
		"HERTZ_PUBLIC_IP=" + s.PublicIP,
		"HERTZ_INGEST_ALLOWED_CIDRS=" + s.IngestAllowed,
		"HERTZ_MAX_SESSIONS=" + strconv.Itoa(s.MaxSessions),
		"HERTZ_MAX_SESSIONS_PER_IP=" + strconv.Itoa(s.MaxSessionsPerIP),
		"HERTZ_LOG_LEVEL=info",
		"HERTZ_PPROF=false",
	}
	if s.TurnSecret != "" {
		env = append(env,
			"HERTZ_TURN_REST_SECRET="+s.TurnSecret,
			"HERTZ_TURN_URLS="+s.TurnURLs,
			"HERTZ_TURN_TTL="+strconv.Itoa(s.TurnTTLHrs)+"h",
		)
	}
	return env
}

func (s Settings) listenerBaseURL() string {
	return s.publicURL("/listen")
}

func (s Settings) audioURL() string {
	return s.publicURL("/live.ogg")
}

func (s Settings) metadataURL() string {
	return s.publicURL("/v1/public/status")
}

func (s Settings) publicURL(path string) string {
	host := s.PublicHost
	if host == "" {
		host = "127.0.0.1"
	}
	port := strconv.Itoa(s.HTTPPort)
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: path}).String()
}

func (s Settings) publisherAddress() string {
	return net.JoinHostPort(s.PublicHost, strconv.Itoa(s.IngestPort))
}

func localIPv4() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			return addr.IP.String()
		}
	}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip := ipnet.IP.To4(); ip != nil {
					return ip.String()
				}
			}
		}
	}
	return "127.0.0.1"
}

// localSubnetCIDR returns the /24-style network of the first active LAN
// interface, used to prefill HERTZ_INGEST_ALLOWED_CIDRS.
func localSubnetCIDR() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			ones, bits := ipnet.Mask.Size()
			if bits != 32 || ones < 8 {
				continue
			}
			network := ip.Mask(ipnet.Mask)
			return fmt.Sprintf("%s/%d", network.String(), ones)
		}
	}
	return ""
}

func adminAddr() string {
	if addr := strings.TrimSpace(os.Getenv("HERTZ_ADMIN_ADDR")); addr != "" {
		return addr
	}
	return adminAddrDefault
}

func adminPort() int {
	_, port, err := net.SplitHostPort(adminAddr())
	if err != nil {
		return 8090
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 8090
	}
	return n
}
