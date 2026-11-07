// Package auth implements short-lived HMAC-signed bearer tokens for the HERTZ
// signaling plane. It is intentionally dependency-free so the token format can
// be issued by any upstream application (Go, Node, Python, ...).
//
// Token format:
//
//	base64url(payload JSON) + "." + base64url(HMAC-SHA256(payload))
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies what a token authorizes.
type Kind string

const (
	KindSubscribe Kind = "sub"
	KindPublish   Kind = "pub"
)

// Claims is the JSON payload inside a token.
type Claims struct {
	Subject   string `json:"sub"`
	Stream    string `json:"stream"`
	Kind      Kind   `json:"kind"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	JTI       string `json:"jti,omitempty"`
}

var (
	ErrMalformed = errors.New("malformed token")
	ErrSignature = errors.New("bad token signature")
	ErrExpired   = errors.New("token expired")
	ErrAudience  = errors.New("token audience mismatch")
)

// Signer creates and verifies tokens.
type Signer struct {
	secret   []byte
	insecure bool // local development only: accepts the literal token "dev"
}

// NewSigner returns a signer. If insecure is true and secret is empty, the
// literal token "dev" is accepted and all claims are synthesized by Verify.
func NewSigner(secret string, insecure bool) (*Signer, error) {
	if secret == "" && !insecure {
		return nil, errors.New("empty auth secret")
	}
	if secret != "" && len(secret) < 16 {
		return nil, errors.New("auth secret must be at least 16 bytes")
	}
	return &Signer{secret: []byte(secret), insecure: insecure}, nil
}

// Sign serializes and signs claims. It fills IssuedAt when zero.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.Subject == "" || c.Stream == "" || (c.Kind != KindSubscribe && c.Kind != KindPublish) {
		return "", errors.New("invalid claims")
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Now().Unix()
	}
	if c.ExpiresAt == 0 {
		c.ExpiresAt = time.Now().Add(15 * time.Minute).Unix()
	}
	if c.ExpiresAt <= c.IssuedAt {
		return "", errors.New("exp must be after iat")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// Verify checks the signature, expiry, kind and stream. It returns the claims
// on success. The expected stream may be empty to accept any stream.
func (s *Signer) Verify(token string, kind Kind, stream string) (Claims, error) {
	if s.insecure && (token == "" || token == "dev") {
		if stream == "" {
			stream = "*"
		}
		return Claims{
			Subject:   "dev",
			Stream:    stream,
			Kind:      kind,
			IssuedAt:  time.Now().Unix(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, nil
	}

	payloadPart, sigPart, ok := strings.Cut(token, ".")
	if !ok || payloadPart == "" || sigPart == "" {
		return Claims{}, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payloadPart))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, ErrSignature
	}
	body, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(body, &c); err != nil {
		return Claims{}, ErrMalformed
	}
	now := time.Now().Unix()
	if c.ExpiresAt <= now-30 { // 30 s clock skew
		return Claims{}, ErrExpired
	}
	if c.Kind != kind {
		return Claims{}, fmt.Errorf("%w: want %s got %s", ErrAudience, kind, c.Kind)
	}
	if stream != "" && c.Stream != stream && c.Stream != "*" {
		return Claims{}, fmt.Errorf("%w: want stream %q got %q", ErrAudience, stream, c.Stream)
	}
	return c, nil
}

// NewID returns a cryptographically random 128-bit identifier encoded as hex.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b[:])
}
