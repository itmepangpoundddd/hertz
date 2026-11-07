package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func validSettings() Settings {
	s := defaultSettings()
	s.PublicHost = "192.168.1.10"
	s.AuthSecret = strings.Repeat("ab", 32)
	return s.normalized()
}

func TestDataDirPrefersLocalAppData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("LOCALAPPDATA is Windows-specific")
	}
	local := t.TempDir()
	t.Setenv("HERTZ_ADMIN_DATA", "")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(local, "roaming"))
	got := dataDir()
	want := filepath.Join(local, "HERTZ")
	if got != want {
		t.Fatalf("dataDir()=%q, want %q", got, want)
	}
}

func TestDataDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERTZ_ADMIN_DATA", dir)
	if got := dataDir(); got != dir {
		t.Fatalf("dataDir()=%q, want %q", got, dir)
	}
}

// Regression test: validate used to have a value receiver and set PublicHost on
// a copy, so the default never reached config.json.
func TestNormalizedFillsPublicHost(t *testing.T) {
	s := validSettings()
	s.PublicHost = "   "
	got := s.normalized()
	if strings.TrimSpace(got.PublicHost) == "" {
		t.Fatal("normalized() left PublicHost empty")
	}
	if err := got.validate(); err != nil {
		t.Fatalf("normalized defaults should validate: %v", err)
	}
}

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Settings)
		want   string
	}{
		{"duplicate ports", func(s *Settings) { s.MetricsPort = s.HTTPPort }, "unique"},
		{"admin port", func(s *Settings) { s.HTTPPort = adminPort() }, "admin page"},
		{"bad stream", func(s *Settings) { s.StreamID = "has space" }, "Stream ID"},
		{"bad cidr", func(s *Settings) { s.IngestAllowed = "not-a-cidr" }, "CIDR"},
		{"bad public ip", func(s *Settings) { s.PublicIP = "example.com" }, "Public IP"},
		{"short secret", func(s *Settings) { s.AuthSecret = "short" }, "secret"},
		{"bad turn url", func(s *Settings) { s.TurnURLs = "https://example.com"; s.TurnSecret = strings.Repeat("cd", 16) }, "TURN URL"},
		{"turn without urls", func(s *Settings) { s.TurnSecret = strings.Repeat("cd", 16) }, "TURN URL"},
		{"public host with port", func(s *Settings) { s.PublicHost = "example.com:8080" }, "port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := validSettings()
			tc.modify(&s)
			err := s.validate()
			if err == nil {
				t.Fatalf("validate() accepted invalid settings %+v", s)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestListenerBaseURL(t *testing.T) {
	s := validSettings()
	s.PublicHost = "192.168.1.10"
	s.HTTPPort = 8080
	if got, want := s.listenerBaseURL(), "http://192.168.1.10:8080/listen"; got != want {
		t.Fatalf("listenerBaseURL()=%q, want %q", got, want)
	}
	s.PublicHost = "::1"
	s.HTTPPort = 8080
	if got, want := s.listenerBaseURL(), "http://[::1]:8080/listen"; got != want {
		t.Fatalf("IPv6 listenerBaseURL()=%q, want %q", got, want)
	}
	s.PublicHost = "[::1]"
	if got, want := s.normalized().listenerBaseURL(), "http://[::1]:8080/listen"; got != want {
		t.Fatalf("bracketed IPv6 listenerBaseURL()=%q, want %q", got, want)
	}
}

func TestEngineEnvTurn(t *testing.T) {
	s := validSettings()
	s.TurnSecret = ""
	for _, kv := range s.engineEnv() {
		if strings.HasPrefix(kv, "HERTZ_TURN_") {
			t.Fatalf("engineEnv leaked TURN env without a secret: %q", kv)
		}
	}
	s.TurnURLs = "turn:turn.example.com:3478?transport=udp"
	s.TurnSecret = strings.Repeat("cd", 16)
	var found int
	for _, kv := range s.engineEnv() {
		if strings.HasPrefix(kv, "HERTZ_TURN_") {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("engineEnv TURN entries=%d, want 3", found)
	}
}

func TestEngineEnvMetricsBinding(t *testing.T) {
	s := validSettings()
	s.MetricsPort = 9090
	want := "HERTZ_METRICS_ADDR=127.0.0.1:9090"
	if !containsEnv(s.engineEnv(), want) {
		t.Fatalf("default metrics env missing %q", want)
	}
	s.MetricsPublic = true
	want = "HERTZ_METRICS_ADDR=:9090"
	if !containsEnv(s.engineEnv(), want) {
		t.Fatalf("public metrics env missing %q", want)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestPublisherAddress(t *testing.T) {
	s := validSettings()
	s.PublicHost = "10.0.0.5"
	s.IngestPort = 5004
	if got, want := s.publisherAddress(), "10.0.0.5:5004"; got != want {
		t.Fatalf("publisherAddress()=%q, want %q", got, want)
	}
}

func TestHostIsLoopback(t *testing.T) {
	for _, host := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090", "localhost"} {
		if !hostIsLoopback(host) {
			t.Fatalf("hostIsLoopback(%q)=false", host)
		}
	}
	for _, host := range []string{"evil.example:8090", "192.168.1.2:8090", ""} {
		if hostIsLoopback(host) {
			t.Fatalf("hostIsLoopback(%q)=true", host)
		}
	}
}

func TestREMOTELoopback(t *testing.T) {
	if !remoteIsLoopback("127.0.0.1:1234") || !remoteIsLoopback("[::1]:1234") {
		t.Fatal("loopback remote addresses rejected")
	}
	if remoteIsLoopback("192.168.1.2:1234") {
		t.Fatal("non-loopback remote address accepted")
	}
}

func TestPortAvailable(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := portAvailable("tcp4", port); err == nil {
		t.Fatalf("portAvailable(tcp4, %d) succeeded while the port is in use", port)
	}
}

func TestRotateLogReplacesOldBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hertz.log")
	if err := os.WriteFile(path, []byte("new-log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("old-log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rotateLog(path, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active log still exists: %v", err)
	}
	raw, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new-log" {
		t.Fatalf("backup=%q, want new-log", raw)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERTZ_ADMIN_DATA", dir)
	s := validSettings()
	s.SetupComplete = true
	if err := saveSettings(s); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, s)
	}
	if st, err := os.Stat(settingsPath()); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions are too broad: %v", st.Mode().Perm())
	}
}
