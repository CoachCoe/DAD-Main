package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

type applicationReceipts struct{ proof string }

func (s *applicationReceipts) ApplyWithReceipt(_ context.Context, _ pgtype.UUID, _ store.PartnerApplicationInput, receipt string) error {
	s.proof = receipt
	return nil
}
func (s *applicationReceipts) ApplicationStatus(_ context.Context, receipt string) (store.ApplicationStatus, error) {
	if receipt != s.proof {
		return store.ApplicationStatus{}, store.ErrPartnerDenied
	}
	return store.ApplicationStatus{Organization: "Synthetic", Status: "PENDING"}, nil
}

func TestApplicationReceiptCookieAndRoleSeparation(t *testing.T) {
	proof := strings.Repeat("r", 43)
	source := &applicationReceipts{}
	server := NewServer("", Services{ApplicationReceipts: source})
	r := httptest.NewRequest("POST", "/donor/partner/applications", strings.NewReader(`{"application":{},"receipt":"`+proof+`"}`))
	r.Header.Set("Origin", testPublicOrigin)
	r.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-000000000001")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	cookies := response.Result().Cookies()
	if response.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Name != applicationCookie || cookies[0].Value != proof ||
		!cookies[0].HttpOnly ||
		cookies[0].SameSite != http.SameSiteLaxMode ||
		cookies[0].MaxAge != 30*24*60*60 ||
		strings.Contains(response.Body.String(), proof) {
		t.Fatalf("receipt response: %d %s", response.Code, response.Body)
	}
	for _, cookieName := range []string{applicationCookie, partnerCookie, donorCookie} {
		r := httptest.NewRequest("GET", "/donor/partner/application-status", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: proof})
		response = httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		want := http.StatusUnauthorized
		if cookieName == applicationCookie {
			want = http.StatusOK
		}
		if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("receipt read with %s: %d %v", cookieName, response.Code, response.Header())
		}
	}
	r = appRequest("GET", "/donor/partner/application-status", "")
	r.Header.Set("Authorization", "Bearer "+proof)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("native receipt read: %d", response.Code)
	}
}

func applyRequest(proof string) *http.Request {
	r := httptest.NewRequest("POST", "/donor/partner/applications", strings.NewReader(`{"application":{},"receipt":"`+proof+`"}`))
	r.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-000000000001")
	return r
}

func TestNativeApplicationReceiptIsKeptByTheApp(t *testing.T) {
	proof := strings.Repeat("n", 43)
	source := &applicationReceipts{}
	server := NewServer("", Services{ApplicationReceipts: source})
	r := applyRequest(proof)
	r.Header.Set("X-DAD-Client", "native")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK || response.Header().Get("Set-Cookie") != "" || source.proof != proof ||
		strings.TrimSpace(response.Body.String()) != `{"received":true}` {
		t.Fatalf("native apply: %d %v %s", response.Code, response.Header(), response.Body)
	}
	r = httptest.NewRequest("GET", "/donor/partner/application-status", nil)
	r.Header.Set("X-DAD-Client", "native")
	r.Header.Set("Authorization", "Bearer "+proof)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"PENDING"`) ||
		response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("native status read: %d %s", response.Code, response.Body)
	}
}

func TestApplicationReceiptRefusesOtherOriginsAndMixedClients(t *testing.T) {
	proof := strings.Repeat("o", 43)
	server := NewServer("", Services{ApplicationReceipts: &applicationReceipts{}})
	for name, mark := range map[string]func(*http.Request){
		"no client marker": func(*http.Request) {},
		"foreign origin":   func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.org") },
		"app with origin": func(r *http.Request) {
			r.Header.Set("X-DAD-Client", "native")
			r.Header.Set("Origin", "https://evil.example.org")
		},
		"app with cookie": func(r *http.Request) {
			r.Header.Set("X-DAD-Client", "native")
			r.AddCookie(&http.Cookie{Name: donorCookie, Value: "session"})
		},
	} {
		r := applyRequest(proof)
		mark(r)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		if response.Code != http.StatusForbidden || response.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: got %d %v", name, response.Code, response.Header())
		}
	}
}

func TestNativeApplicationsShareTheVisitorBudget(t *testing.T) {
	server := NewServer("", Services{ApplicationReceipts: &applicationReceipts{}})
	var limited *httptest.ResponseRecorder
	for range visitorLimit.limit + 1 {
		r := applyRequest(strings.Repeat("l", 43))
		r.Header.Set("X-DAD-Client", "native")
		limited = httptest.NewRecorder()
		server.Handler.ServeHTTP(limited, r)
	}
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" ||
		!strings.Contains(limited.Body.String(), "Too many requests") {
		t.Fatalf("after %d native applications: %d %v %s", visitorLimit.limit+1, limited.Code, limited.Header(), limited.Body)
	}
}
