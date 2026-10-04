package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type replyFunc func(http.ResponseWriter, int, interface{})

func (g *browserGuard) check(w http.ResponseWriter, r *http.Request, reply replyFunc) bool {
	if status := g.allow(r, time.Now()); status != 0 {
		detail := "Request origin is not allowed"
		if status == 429 {
			detail = "Too many requests"
			w.Header().Set("Retry-After", "60")
		}
		reply(w, status, map[string]string{"detail": detail})
		return false
	}
	return true
}
func decodeBody(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	if value := r.Header.Get("Content-Type"); value != "" {
		media, _, err := mime.ParseMediaType(value)
		if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
			return false
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	var extra interface{}
	return decoder.Decode(&extra) == io.EOF
}
func sessionToken(r *http.Request) string {
	if cookie, err := r.Cookie("dad_donor_session"); err == nil {
		return cookie.Value
	}
	return ""
}

// This is the existing per-process 20/minute protection, not a distributed
// production quota. Forwarded address headers are not trusted implicitly.
type browserGuard struct {
	mu     sync.Mutex
	origin string
	seen   map[string][]time.Time
}

func (g *browserGuard) allow(r *http.Request, now time.Time) int {
	if r.Header.Get("Origin") != g.origin {
		return http.StatusForbidden
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = map[string][]time.Time{}
	}
	if len(g.seen) > 512 {
		for key, hits := range g.seen {
			if len(hits) == 0 || now.Sub(hits[len(hits)-1]) > time.Minute {
				delete(g.seen, key)
			}
		}
	}
	hits := g.seen[peer]
	for len(hits) > 0 && now.Sub(hits[0]) > time.Minute {
		hits = hits[1:]
	}
	if len(hits) >= 20 {
		return http.StatusTooManyRequests
	}
	g.seen[peer] = append(hits, now)
	return 0
}
