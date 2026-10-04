package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
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
	return decodeBodyLimit(w, r, target, 256*1024)
}
func decodeBodyLimit(w http.ResponseWriter, r *http.Request, target interface{}, limit int64) bool {
	if value := r.Header.Get("Content-Type"); value != "" {
		media, _, err := mime.ParseMediaType(value)
		if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
			return false
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
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

// Only explicitly trusted proxy peers may provide a single client address.
type browserGuard struct {
	mu             sync.Mutex
	origin         string
	seen           map[string][]time.Time
	trustedProxies []netip.Prefix
	nextSweep      time.Time
}

const maxRatePeers = 65536

func (g *browserGuard) allow(r *http.Request, now time.Time) int {
	if r.Header.Get("Origin") != g.origin {
		return http.StatusForbidden
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if address, err := netip.ParseAddr(peer); err == nil {
		address = address.Unmap()
		peer = address.String()
		for _, prefix := range g.trustedProxies {
			if !prefix.Contains(address) {
				continue
			}
			if client, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
				peer = client.Unmap().String()
			}
			break
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = map[string][]time.Time{}
	}
	if now.After(g.nextSweep) {
		for key, hits := range g.seen {
			if len(hits) == 0 || now.Sub(hits[len(hits)-1]) > time.Minute {
				delete(g.seen, key)
			}
		}
		g.nextSweep = now.Add(time.Minute)
	}
	if _, exists := g.seen[peer]; !exists && len(g.seen) >= maxRatePeers {
		return http.StatusTooManyRequests
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
