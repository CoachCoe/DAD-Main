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
