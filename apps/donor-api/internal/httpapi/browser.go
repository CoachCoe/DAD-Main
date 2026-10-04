package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

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
