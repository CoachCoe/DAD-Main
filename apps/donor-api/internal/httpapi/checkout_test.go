package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store"
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

func TestCheckoutRejectsInvalidRequestsBeforeTheProcessor(t *testing.T) {
	valid := `{"email":"donor@example.org","amount_minor":3000}`
	for _, tc := range []struct {
		name, body string
		header     map[string]string
		status     int
	}{
		{"boolean amount", `{"email":"donor@example.org","amount_minor":true}`, nil, 422},
		{"fractional amount", `{"email":"donor@example.org","amount_minor":3000.0}`, nil, 422},
		{"string amount", `{"email":"donor@example.org","amount_minor":"3000"}`, nil, 422},
		{"null amount", `{"email":"donor@example.org","amount_minor":null}`, nil, 422},
		{"null email", `{"email":null,"amount_minor":3000}`, nil, 422},
		{"null frequency", `{"email":"donor@example.org","amount_minor":3000,"frequency":null}`, nil, 422},
		{"unknown frequency", `{"email":"donor@example.org","amount_minor":3000,"frequency":"WEEKLY"}`, nil, 422},
		{"unknown field", `{"email":"donor@example.org","amount_minor":3000,"unexpected":"secret"}`, nil, 422},
		{"trailing value", valid + ` {}`, nil, 422},
		{"amount over ceiling by a cent", `{"email":"donor@example.org","amount_minor":90001}`, nil, 422},
		{"amount at $31 a day", `{"email":"donor@example.org","amount_minor":93000}`, nil, 422},
		{"amount under minimum", `{"email":"donor@example.org","amount_minor":99}`, nil, 422},
		{"amount under $1 a day", `{"email":"donor@example.org","amount_minor":2999}`, nil, 422},
		{"amount not whole dollars a day", `{"email":"donor@example.org","amount_minor":4500}`, nil, 422},
		{"missing key", valid, map[string]string{"Idempotency-Key": ""}, 422},
		{"version 1 key", valid, map[string]string{"Idempotency-Key": "8f4f52bb-99f6-190e-8313-5156cc4f8b45"}, 422},
		{"text body", valid, map[string]string{"Content-Type": "text/plain"}, 415},
		{"oversized body", `{"email":"` + strings.Repeat("a", maxJSONBody) + `"}`, nil, 413},
		{"foreign origin", valid, map[string]string{"Origin": "https://evil.example.org"}, 403},
	} {
		stub := &checkoutStub{}
		r := checkoutRequest(tc.body)
		for name, value := range tc.header {
			r.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		NewServer("", Services{Checkout: stub}).Handler.ServeHTTP(response, r)
		if response.Code != tc.status || stub.calls != 0 {
			t.Errorf("%s: got %d after %d processor calls, want %d before any", tc.name, response.Code, stub.calls, tc.status)
		}
		if strings.Contains(response.Body.String(), "secret") {
			t.Errorf("%s: response echoed request content", tc.name)
		}
	}
}

func TestGuestCheckoutCarriesReferralAndDefaultsToMonthly(t *testing.T) {
	stub := &checkoutStub{}
	r := checkoutRequest(`{"email":"donor@example.org","amount_minor":3000}`)
	r.AddCookie(&http.Cookie{Name: "dad_referral", Value: "visitor"})
	response := httptest.NewRecorder()
	NewServer("", Services{Checkout: stub}).Handler.ServeHTTP(response, r)
	if response.Code != 201 || stub.request.Frequency != "MONTHLY" || stub.request.Visitor == nil || *stub.request.Visitor != "visitor" {
		t.Fatalf("got %d %+v, want 201 with a monthly request carrying the referral visitor", response.Code, stub.request)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("checkout response headers: %v", response.Header())
	}
}

func TestCheckoutAcceptsTheGiftCeiling(t *testing.T) {
	stub := &checkoutStub{}
	r := checkoutRequest(`{"email":"donor@example.org","amount_minor":90000}`)
	response := httptest.NewRecorder()
	NewServer("", Services{Checkout: stub}).Handler.ServeHTTP(response, r)
	if response.Code != 201 || stub.request.Amount != 90000 {
		t.Fatalf("got %d amount %d, want 201 at the $900/mo ceiling", response.Code, stub.request.Amount)
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
			t.Errorf("%v: got %d %s, want %d without processor detail", tc.err, response.Code, response.Body, tc.status)
		}
	}
}
