package signaling

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"

	"hertz/internal/auth"
	"hertz/internal/config"
)

func TestResolveStreamID(t *testing.T) {
	if got, err := resolveStreamID(auth.Claims{Stream: "*"}, "main", "default"); err != nil || got != "main" {
		t.Fatalf("wildcard requested: got %q err %v", got, err)
	}
	if got, err := resolveStreamID(auth.Claims{Stream: "*"}, "", "default"); err != nil || got != "default" {
		t.Fatalf("wildcard default: got %q err %v", got, err)
	}
	if got, err := resolveStreamID(auth.Claims{Stream: "main"}, "", "default"); err != nil || got != "main" {
		t.Fatalf("scoped default: got %q err %v", got, err)
	}
	if _, err := resolveStreamID(auth.Claims{Stream: "main"}, "other", "default"); err == nil {
		t.Fatal("scoped token allowed another stream")
	}
	if _, err := resolveStreamID(auth.Claims{Stream: "*"}, "*", "default"); err == nil {
		t.Fatal("literal * request accepted")
	}
}

func TestClientIPUsesRightmostTrustedHop(t *testing.T) {
	s := &Server{cfg: config.Config{TrustProxy: true, TrustedProxyHops: 1}}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.10:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.7, 192.0.2.5")
	if got := s.clientIP(r); got != "192.0.2.5" {
		t.Fatalf("hops=1 got %q want 192.0.2.5", got)
	}
	s.cfg.TrustedProxyHops = 2
	if got := s.clientIP(r); got != "198.51.100.7" {
		t.Fatalf("hops=2 got %q want 198.51.100.7", got)
	}
	s.cfg.TrustedProxyHops = 9
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("hops=9 got %q want 203.0.113.9", got)
	}
}

func TestBearerTokenRejectsQueryString(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/config?token=dev", nil)
	if got := bearerToken(r); got != "" {
		t.Fatalf("query token accepted: %q", got)
	}
	r.Header.Set("Authorization", "Bearer header-token")
	if got := bearerToken(r); got != "header-token" {
		t.Fatalf("header token=%q", got)
	}
}

func TestBrowserICEServersDoesNotLeakStaticTURN(t *testing.T) {
	s := &Server{cfg: config.Config{
		ICEServers: []config.ICEServer{
			{URLs: []string{"stun:stun.example.net:3478"}},
			{URLs: []string{"turn:turn.example.net:3478"}, Username: "static-user", Credential: "static-secret"},
		},
	}}
	got := s.browserICEServers(auth.Claims{Subject: "alice"})
	if len(got) != 1 || len(got[0].URLs) != 1 || got[0].URLs[0] != "stun:stun.example.net:3478" {
		t.Fatalf("unexpected browser ICE servers: %+v", got)
	}
	if got[0].Username != "" || got[0].Credential != "" {
		t.Fatalf("static credentials leaked: %+v", got[0])
	}
}

func TestBrowserICEServersIncludesEphemeralTURN(t *testing.T) {
	s := &Server{
		cfg:  config.Config{TURNRESTSecret: "0123456789abcdef0123456789abcdef", TURNRESTURLs: []string{"turn:turn.example.net:3478"}, TURNCredentialTTL: time.Hour},
		turn: &turnREST{secret: "0123456789abcdef0123456789abcdef", urls: []string{"turn:turn.example.net:3478"}, ttl: time.Hour},
	}
	got := s.browserICEServers(auth.Claims{Subject: "alice"})
	if len(got) != 1 || got[0].Username == "" || got[0].Credential == "" {
		t.Fatalf("missing ephemeral TURN credentials: %+v", got)
	}
	if got[0].Credential == s.cfg.TURNRESTSecret {
		t.Fatal("static TURN secret leaked as credential")
	}
}

func TestTURNRESTCredentials(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	now := time.Unix(1_700_000_000, 0)
	tr := &turnREST{secret: secret, urls: []string{"turn:turn.example.net:3478"}, ttl: time.Hour}
	user, pass := tr.credentials("alice", now)
	wantUser := "1700003600:alice"
	if user != wantUser {
		t.Fatalf("username=%q want %q", user, wantUser)
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(wantUser))
	wantPass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if pass != wantPass {
		t.Fatalf("password mismatch")
	}
}
