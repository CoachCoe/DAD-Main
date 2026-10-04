package httpapi

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestTrustedProxySeparatesClientsWithoutTrustingOtherPeers(t *testing.T) {
	g := &browserGuard{origin: "http://localhost:3000", trustedProxies: []netip.Prefix{netip.MustParsePrefix("10.20.0.2/32")}}
	r := checkoutRequest(`{}`)
	r.RemoteAddr = "10.20.0.2:1234"
	now := time.Now()
	for _, ip := range []string{"203.0.113.1", "203.0.113.2", "2001:db8::1"} {
		r.Header.Set("X-Real-IP", ip)
		for range 20 {
			if g.allow(r, now) != 0 {
				t.Fatal("proxy clients shared a quota")
			}
		}
		if g.allow(r, now) != 429 {
			t.Fatal("client quota bypassed")
		}
	}
	r.RemoteAddr = "10.20.0.3:1234"
	for i := range 21 {
		r.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", i))
		want := 0
		if i == 20 {
			want = 429
		}
		if g.allow(r, now) != want {
			t.Fatal("untrusted peer spoofed a client")
		}
	}
	r.RemoteAddr = "10.20.0.2:1234"
	for _, invalid := range []string{"203.0.113.1, 203.0.113.2", "unknown", "203.0.113.1:42"} {
		r.Header.Set("X-Real-IP", invalid)
		if g.allow(r, now) != 0 {
			t.Fatal("invalid address did not fall back to peer")
		}
	}
	if len(g.seen) != 5 {
		t.Fatal("invalid addresses became distinct buckets")
	}
	r.Header.Set("Origin", "https://attacker.example")
	if g.allow(r, now) != http.StatusForbidden {
		t.Fatal("proxy bypassed origin protection")
	}
}

func TestRateLimiterBoundsMemoryAndRecoversAfterExpiry(t *testing.T) {
	g := &browserGuard{origin: "http://localhost:3000"}
	r := checkoutRequest(`{}`)
	now := time.Now()
	for i := range maxRatePeers {
		r.RemoteAddr = fmt.Sprintf("198.18.%d.%d:1", i/256, i%256)
		if g.allow(r, now) != 0 {
			t.Fatal("capacity reached early")
		}
	}
	r.RemoteAddr = "203.0.113.8:1"
	if g.allow(r, now) != 429 || len(g.seen) != maxRatePeers {
		t.Fatal("unbounded rate limiter")
	}
	if g.allow(r, now.Add(time.Minute+time.Nanosecond)) != 0 || len(g.seen) != 1 {
		t.Fatal("expired buckets were not reclaimed")
	}
}
