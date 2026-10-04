package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type checkoutStub struct {
	calls   int
	request payments.CheckoutRequest
	err     error
}

func (s *checkoutStub) BeginCheckout(ctx context.Context, r payments.CheckoutRequest) (payments.CheckoutResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded checkout")
	}
	s.calls++
	s.request = r
	return payments.CheckoutResult{CheckoutID: r.RequestID.String(), URL: "https://checkout.stripe.com/c/test"}, s.err
}
func checkoutRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "/donor/checkouts", strings.NewReader(body))
	r.Header.Set("Origin", "http://localhost:3000")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "8f4f52bb-99f6-490e-8313-5156cc4f8b45")
	return r
}
func TestCheckoutStrictInputAndOrigin(t *testing.T) {
	for _, body := range []string{`{"email":"donor@example.org","amount_minor":true}`, `{"email":"donor@example.org","amount_minor":3000.0}`, `{"email":"donor@example.org","amount_minor":"3000"}`, `{"email":"donor@example.org","amount_minor":null}`, `{"email":null,"amount_minor":3000}`, `{"email":"donor@example.org","amount_minor":3000,"frequency":null}`, `{"email":"donor@example.org","amount_minor":3000,"unexpected":"secret"}`, `{"email":"donor@example.org","amount_minor":3000} {}`, `{"email":"donor@example.org","amount_minor":999999999}`} {
		stub := &checkoutStub{}
		server := NewServer("", Services{Checkout: stub})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, checkoutRequest(body))
		if response.Code != 422 || stub.calls != 0 || strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("bad request accepted: %s %d", body, response.Code)
		}
	}
	stub := &checkoutStub{}
	server := NewServer("", Services{Checkout: stub})
	r := checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`)
	r.Header.Set("Origin", "https://evil.example.org")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != 403 || stub.calls != 0 {
		t.Fatal("cross-origin checkout accepted")
	}
	r = checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`)
	r.AddCookie(&http.Cookie{Name: "dad_referral", Value: "visitor"})
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != 201 || stub.request.Frequency != "MONTHLY" || stub.request.Visitor == nil || *stub.request.Visitor != "visitor" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("guest checkout contract changed")
	}
}
func TestCheckoutErrorsArePrivate(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{payments.ErrCheckoutConflict, 409}, {payments.ErrCheckoutInput, 422}, {store.ErrDenied, 403}, {errors.New("private processor details"), 503}} {
		server := NewServer("", Services{Checkout: &checkoutStub{err: tc.err}})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`))
		if response.Code != tc.status || strings.Contains(response.Body.String(), "private processor") {
			t.Fatal("error contract changed")
		}
	}
}

func TestCheckoutRefusesNonJSONContentType(t *testing.T) {
	stub := &checkoutStub{}
	server := NewServer("", Services{Checkout: stub})
	r := checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`)
	r.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != 422 || stub.calls != 0 {
		t.Fatal("non-JSON request accepted")
	}
}
func TestBrowserLimitUsesPeerNotForwardedHeaders(t *testing.T) {
	guard := &browserGuard{origin: "http://localhost:3000"}
	now := time.Now()
	r := checkoutRequest(`{}`)
	for i := 0; i < 20; i++ {
		if guard.allow(r, now) != 0 {
			t.Fatal("limit too early")
		}
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.12")
	if guard.allow(r, now) != 429 || guard.allow(r, now.Add(time.Minute)) != 429 || guard.allow(r, now.Add(time.Minute+time.Nanosecond)) != 0 {
		t.Fatal("rate window or peer contract changed")
	}
}
