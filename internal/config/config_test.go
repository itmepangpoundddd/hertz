package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultsAreProductionSafe(t *testing.T) {
	// Empty values exercise defaults; the process may have inherited env vars,
	// so override every key this test asserts.
	t.Setenv("HERTZ_ALLOW_INSECURE_DEV", "true")
	t.Setenv("HERTZ_ICE_LOOPBACK", "")
	t.Setenv("HERTZ_QUEUE_SIZE", "")
	t.Setenv("HERTZ_STATS_INTERVAL", "")
	t.Setenv("HERTZ_AUTH_SECRET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IncludeLoop {
		t.Fatal("HERTZ_ICE_LOOPBACK default must be false")
	}
	if cfg.QueueSize != 10 {
		t.Fatalf("queue=%d want 10", cfg.QueueSize)
	}
	if cfg.StatsInterval != 5*time.Second {
		t.Fatalf("stats interval=%v want 5s", cfg.StatsInterval)
	}
	if cfg.SharedPayload {
		t.Fatal("HERTZ_SHARED_PAYLOAD must default to false")
	}
}

func TestStaticBrowserTURNCredentialsRejected(t *testing.T) {
	t.Setenv("HERTZ_ALLOW_INSECURE_DEV", "true")
	t.Setenv("HERTZ_BROWSER_ICE_SERVERS",
		`[{"urls":["turn:turn.example.net:3478"],"username":"u","credential":"p"}]`)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "static TURN") {
		t.Fatalf("err=%v want static TURN rejection", err)
	}
}

func TestTURNRESTRequiresURLs(t *testing.T) {
	t.Setenv("HERTZ_ALLOW_INSECURE_DEV", "true")
	t.Setenv("HERTZ_TURN_REST_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("HERTZ_TURN_URLS", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "HERTZ_TURN_URLS") {
		t.Fatalf("err=%v want missing TURN URL error", err)
	}
}
