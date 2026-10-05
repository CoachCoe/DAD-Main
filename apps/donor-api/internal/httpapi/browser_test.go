package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

var testClass = rateClass{"test", 20}

func guardedRequest(remote string) *http.Request {
	r := httptest.NewRequest("POST", "/donor/checkouts", nil)
	r.Header.Set("Origin", "http://localhost:3000")
	r.RemoteAddr = remote
	return r
}

func exhaust(t *testing.T, g *browserGuard, r *http.Request, class rateClass, now time.Time) {
	t.Helper()
	for i := range class.limit {
		if status, _ := g.allow(r, class, now); status != 0 {
			t.Fatalf("request %d rejected with %d before the limit", i+1, status)
		}
	}
	if status, _ := g.allow(r, class, now); status != http.StatusTooManyRequests {
		t.Fatalf("request over the limit got %d, want 429", status)
	}
}

func TestTrustedProxySeparatesClientsWithoutTrustingOtherPeers(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", []netip.Prefix{netip.MustParsePrefix("10.20.0.2/32")})
	now := time.Now()
	r := guardedRequest("10.20.0.2:1234")
	for _, ip := range []string{"203.0.113.1", "203.0.113.2", "2001:db8::1"} {
		r.Header.Set("X-Real-IP", ip)
		exhaust(t, g, r, testClass, now)
	}

	untrusted := guardedRequest("10.20.0.3:1234")
	for i := range testClass.limit {
		untrusted.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", i))
		if status, _ := g.allow(untrusted, testClass, now); status != 0 {
			t.Fatalf("untrusted peer limited early: %d", status)
		}
	}
	untrusted.Header.Set("X-Real-IP", "192.0.2.200")
	if status, _ := g.allow(untrusted, testClass, now); status != http.StatusTooManyRequests {
		t.Fatal("untrusted peer escaped its quota by spoofing X-Real-IP")
	}

	for _, invalid := range []string{"203.0.113.1, 203.0.113.2", "unknown", "203.0.113.1:42"} {
		r.Header.Set("X-Real-IP", invalid)
		if status, _ := g.allow(r, testClass, now); status != 0 {
			t.Fatalf("invalid header %q was not attributed to the proxy peer", invalid)
		}
	}
	if len(g.buckets) != 5 {
		t.Fatalf("got %d buckets, want 5: invalid addresses must share the proxy's bucket", len(g.buckets))
	}

	r.Header.Set("Origin", "https://attacker.example")
	if status, _ := g.allow(r, testClass, now); status != http.StatusForbidden {
		t.Fatal("proxy bypassed origin protection")
	}
}

func TestForwardedHeadersAreIgnoredWithoutTrustedProxies(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", nil)
	now := time.Now()
	r := guardedRequest("192.0.2.1:1234")
	exhaust(t, g, r, testClass, now)
	r.Header.Set("X-Forwarded-For", "203.0.113.12")
	r.Header.Set("X-Real-IP", "203.0.113.12")
	if status, _ := g.allow(r, testClass, now); status != http.StatusTooManyRequests {
		t.Fatal("forwarding headers changed the rate-limit key")
	}
}

func TestRateClassesHaveIndependentBudgets(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", nil)
	now := time.Now()
	r := guardedRequest("192.0.2.1:1234")
	exhaust(t, g, r, authLimit, now)
	if status, _ := g.allow(r, visitorLimit, now); status != 0 {
		t.Fatal("exhausting sign-in attempts blocked giving from the same address")
	}
}

func TestRateLimitRefillsAndReportsRetryDelay(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", nil)
	now := time.Now()
	r := guardedRequest("192.0.2.1:1234")
	exhaust(t, g, r, testClass, now)
	_, wait := g.allow(r, testClass, now)
	if interval := time.Minute / time.Duration(testClass.limit); wait <= 0 || wait > interval {
		t.Fatalf("retry delay %v, want within one refill interval %v", wait, interval)
	}
	if status, _ := g.allow(r, testClass, now.Add(wait)); status != 0 {
		t.Fatal("request after the reported delay was rejected")
	}
	if status, _ := g.allow(r, testClass, now.Add(wait)); status != http.StatusTooManyRequests {
		t.Fatal("one refilled token admitted two requests")
	}
}

func TestIPv6ClientsShareTheirSlash64(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", nil)
	now := time.Now()
	exhaust(t, g, guardedRequest("[2001:db8:1:2::1]:1234"), testClass, now)
	if status, _ := g.allow(guardedRequest("[2001:db8:1:2:ffff::9]:1234"), testClass, now); status != http.StatusTooManyRequests {
		t.Fatal("rotating addresses within one /64 escaped the limit")
	}
	if status, _ := g.allow(guardedRequest("[2001:db8:1:3::1]:1234"), testClass, now); status != 0 {
		t.Fatal("a different /64 shared the limit")
	}
}

func TestRateLimiterBoundsMemoryWithoutLockingOutNewClients(t *testing.T) {
	g := newBrowserGuard("http://localhost:3000", nil)
	now := time.Now()
	for i := range maxRatePeers {
		if status, _ := g.allow(guardedRequest(fmt.Sprintf("198.18.%d.%d:1", i/256, i%256)), testClass, now); status != 0 {
			t.Fatal("capacity reached early")
		}
	}
	if status, _ := g.allow(guardedRequest("203.0.113.8:1"), testClass, now); status != 0 {
		t.Fatal("a full table locked out a new client")
	}
	if len(g.buckets) > maxRatePeers {
		t.Fatalf("table grew to %d entries", len(g.buckets))
	}
	if status, _ := g.allow(guardedRequest("203.0.113.9:1"), testClass, now.Add(time.Minute+time.Second)); status != 0 || len(g.buckets) != 1 {
		t.Fatalf("idle buckets were not reclaimed: %d remain", len(g.buckets))
	}
}

func TestRejectedRequestsDoNotReachHandlers(t *testing.T) {
	stub := &checkoutStub{}
	server := NewServer("", Services{Checkout: stub})
	for range visitorLimit.limit + 1 {
		server.Handler.ServeHTTP(httptest.NewRecorder(), checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`))
	}
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d with Retry-After %q", response.Code, response.Header().Get("Retry-After"))
	}
	if stub.calls != visitorLimit.limit {
		t.Fatalf("checkout ran %d times, want %d", stub.calls, visitorLimit.limit)
	}
}
