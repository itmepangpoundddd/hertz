package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	s, err := NewSigner("0123456789abcdef0123456789abcdef", false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Sign(Claims{
		Subject:   "listener-1",
		Stream:    "main",
		Kind:      KindSubscribe,
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, KindSubscribe, "main")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "listener-1" {
		t.Fatalf("subject=%q", c.Subject)
	}
	if _, err := s.Verify(tok, KindPublish, "main"); err == nil {
		t.Fatal("kind mismatch accepted")
	}
	if _, err := s.Verify(tok, KindSubscribe, "other"); err == nil {
		t.Fatal("stream mismatch accepted")
	}
}

func TestTamperedAndExpired(t *testing.T) {
	s, _ := NewSigner("0123456789abcdef0123456789abcdef", false)
	tok, _ := s.Sign(Claims{
		Subject:   "a",
		Stream:    "main",
		Kind:      KindSubscribe,
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", tok)
	}
	flip := byte('A')
	if parts[0][0] == 'A' {
		flip = 'B'
	}
	bad := string(flip) + parts[0][1:] + "." + parts[1]
	if _, err := s.Verify(bad, KindSubscribe, "main"); err == nil {
		t.Fatal("tampered token accepted")
	}

	expired, _ := s.Sign(Claims{
		Subject:   "a",
		Stream:    "main",
		Kind:      KindSubscribe,
		IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := s.Verify(expired, KindSubscribe, "main"); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestInsecureDevToken(t *testing.T) {
	s, err := NewSigner("", true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify("dev", KindSubscribe, "main")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "dev" || c.Stream != "main" {
		t.Fatalf("unexpected claims: %+v", c)
	}
	wild, err := s.Verify("dev", KindSubscribe, "")
	if err != nil {
		t.Fatal(err)
	}
	if wild.Stream != "*" {
		t.Fatalf("dev wildcard stream=%q want *", wild.Stream)
	}
}
