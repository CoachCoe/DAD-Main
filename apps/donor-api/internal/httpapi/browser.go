package httpapi

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// rateClass separates budgets so a burst on one surface cannot starve another
// for visitors sharing an address, such as everyone on an event's Wi-Fi.
type rateClass struct {
	name  string
	limit int // requests per minute, also the burst size
}

var (
	authLimit    = rateClass{"auth", 10}
	visitorLimit = rateClass{"visitor", 60}
	accountLimit = rateClass{"account", 30}
	staffLimit   = rateClass{"staff", 60}
)

const maxRatePeers = 65536

type rateKey struct {
	class  string
	client netip.Addr
}

type bucket struct {
	tokens float64
	last   time.Time
}

// browserGuard enforces the exact browser Origin and per-client rate limits.
// Only explicitly trusted proxy peers may supply the client address.
type browserGuard struct {
	origin         string
	trustedProxies []netip.Prefix

	mu        sync.Mutex
	buckets   map[rateKey]*bucket
	nextSweep time.Time
}

func newBrowserGuard(origin string, trustedProxies []netip.Prefix) *browserGuard {
	return &browserGuard{origin: origin, trustedProxies: trustedProxies, buckets: map[rateKey]*bucket{}}
}

// allow returns 0 when the request may proceed, otherwise the status to send
// and, for 429, how long until a request would be admitted.
func (g *browserGuard) allow(r *http.Request, class rateClass, now time.Time) (int, time.Duration) {
	if r.Header.Get("Origin") != g.origin {
		return http.StatusForbidden, 0
	}
	key := rateKey{class.name, g.client(r)}
	rate := float64(class.limit) / time.Minute.Seconds()

	g.mu.Lock()
	defer g.mu.Unlock()
	if now.After(g.nextSweep) {
		g.sweep(now)
	}
	b, ok := g.buckets[key]
	if !ok {
		if len(g.buckets) >= maxRatePeers {
			g.evictOne()
		}
		b = &bucket{tokens: float64(class.limit), last: now}
		g.buckets[key] = b
	}
	b.tokens = math.Min(float64(class.limit), b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens < 1 {
		return http.StatusTooManyRequests, time.Duration((1 - b.tokens) / rate * float64(time.Second))
	}
	b.tokens--
	return 0, 0
}

func (g *browserGuard) client(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	for _, prefix := range g.trustedProxies {
		if prefix.Contains(peer) {
			if forwarded, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
				peer = forwarded.Unmap()
			}
			break
		}
	}
	// One IPv6 subscriber typically controls a whole /64.
	if peer.Is6() {
		peer = netip.PrefixFrom(peer, 64).Masked().Addr()
	}
	return peer
}

// sweep drops buckets that have refilled; they are indistinguishable from new ones.
func (g *browserGuard) sweep(now time.Time) {
	for key, b := range g.buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(g.buckets, key)
		}
	}
	g.nextSweep = now.Add(time.Minute)
}

// evictOne bounds memory without refusing unseen clients, which would let a
// flood of addresses lock everyone else out.
func (g *browserGuard) evictOne() {
	for key := range g.buckets {
		delete(g.buckets, key)
		return
	}
}

func (a *api) browser(class rateClass, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch status, wait := a.guard.allow(r, class, time.Now()); status {
		case 0:
			next(w, r)
		case http.StatusTooManyRequests:
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			writeError(w, r, status, "Too many requests", nil)
		default:
			writeError(w, r, status, "Request origin is not allowed", nil)
		}
	}
}
