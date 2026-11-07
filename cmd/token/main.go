// Command token issues a short-lived HERTZ signaling token. It is a developer
// tool; in production the upstream app that authenticates users should issue
// tokens with the same token format.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"hertz/internal/auth"
)

func main() {
	var (
		secret = flag.String("secret", os.Getenv("HERTZ_AUTH_SECRET"), "HMAC secret (or HERTZ_AUTH_SECRET)")
		sub    = flag.String("sub", "listener-1", "subject / listener id")
		stream = flag.String("stream", "main", "stream id, or * for any")
		kind   = flag.String("kind", "sub", "token kind: sub or pub")
		ttl    = flag.Duration("ttl", 15*time.Minute, "token lifetime")
	)
	flag.Parse()

	if *secret == "" {
		fmt.Fprintln(os.Stderr, "error: -secret or HERTZ_AUTH_SECRET is required")
		os.Exit(2)
	}
	s, err := auth.NewSigner(*secret, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	now := time.Now()
	tok, err := s.Sign(auth.Claims{
		Subject:   *sub,
		Stream:    *stream,
		Kind:      auth.Kind(*kind),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(*ttl).Unix(),
		JTI:       auth.NewID(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
