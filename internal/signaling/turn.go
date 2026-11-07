package signaling

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"hertz/internal/auth"
	"hertz/internal/config"
)

// turnREST implements the coturn TURN REST API credential convention:
//
//	username = "<unix-expiry>:<subject>"
//	password = base64(HMAC-SHA1(secret, username))
//
// The browser gets short-lived credentials; the static shared secret never
// leaves the server.
type turnREST struct {
	secret string
	urls   []string
	ttl    time.Duration
}

func newTURNREST(cfg config.Config) *turnREST {
	if cfg.TURNRESTSecret == "" {
		return nil
	}
	return &turnREST{secret: cfg.TURNRESTSecret, urls: cfg.TURNRESTURLs, ttl: cfg.TURNCredentialTTL}
}

func (t *turnREST) credentials(subject string, now time.Time) (string, string) {
	username := fmt.Sprintf("%d:%s", now.Add(t.ttl).Unix(), subject)
	mac := hmac.New(sha1.New, []byte(t.secret))
	_, _ = mac.Write([]byte(username))
	return username, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// browserICEServers returns only listener-safe ICE servers. Static TURN
// credentials from the server config are never copied to a browser response.
func (s *Server) browserICEServers(claims auth.Claims) []config.ICEServer {
	servers := append([]config.ICEServer(nil), s.cfg.BrowserICEServers...)

	if s.turn != nil {
		username, credential := s.turn.credentials(claims.Subject, time.Now())
		servers = append(servers, config.ICEServer{
			URLs:       append([]string(nil), s.turn.urls...),
			Username:   username,
			Credential: credential,
		})
	}

	if len(servers) == 0 {
		// Backward-compatible fallback: expose only STUN entries from the
		// server ICE list. TURN entries are intentionally skipped.
		for _, srv := range s.cfg.ICEServers {
			if stunOnly(srv.URLs) {
				servers = append(servers, config.ICEServer{URLs: append([]string(nil), srv.URLs...)})
			}
		}
	}
	return servers
}

func stunOnly(urls []string) bool {
	if len(urls) == 0 {
		return false
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "stun:") && !strings.HasPrefix(u, "stuns:") {
			return false
		}
	}
	return true
}
