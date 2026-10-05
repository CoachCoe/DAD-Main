package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var browserMutations = []string{
	"/donor/checkouts",
	"/donor/referrals/code",
	"/donor/public/partner-signup",
	"/donor/login",
	"/donor/claim",
	"/donor/logout",
	"/donor/logout-all",
	"/donor/me/email-change",
	"/donor/email-change/confirm",
	"/donor/me/preferences",
	"/donor/me/sharing",
	"/donor/me/wallet/apple",
	"/donor/me/wallet/google",
	"/donor/me/recurring/cancel",
	"/donor/me/recurring/pause",
	"/donor/me/recurring/resume",
	"/donor/me/recurring/amount",
	"/donor/me/payment-method/setup",
	"/donor/staff/sign-in",
	"/donor/staff/provision",
	"/donor/staff/activate",
	"/donor/staff/sign-out",
	"/donor/staff/accounts",
	"/donor/staff/accounts/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/disable",
	"/donor/staff/accounts/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/recovery",
	"/donor/staff/accounts/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/role",
	"/donor/staff/donors/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/claim-email",
	"/donor/staff/messages/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/resend",
	"/donor/staff/donors/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/gifts/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/refund",
	"/donor/staff/donors/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/cancellation",
	"/donor/staff/content/sources",
	"/donor/staff/content/publish",
}

// Every browser mutation must reject a foreign or missing Origin before any
// service runs; a nil service would answer 503 instead of 403.
func TestEveryBrowserMutationRequiresOrigin(t *testing.T) {
	server := NewServer("", Services{Origin: "https://dad.example.org"})
	for _, path := range browserMutations {
		for _, origin := range []string{"", "https://evil.example.org"} {
			r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			r.AddCookie(&http.Cookie{Name: donorCookie, Value: "session"})
			r.AddCookie(&http.Cookie{Name: staffCookie, Value: "session"})
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, r)
			if response.Code != http.StatusForbidden {
				t.Errorf("POST %s with origin %q: got %d, want 403", path, origin, response.Code)
			}
		}
	}
}

func TestDonorRoutesRejectAnonymousRequestsBeforeStorage(t *testing.T) {
	server := NewServer("", Services{})
	for _, path := range []string{"/donor/me/dashboard", "/donor/me/profile", "/donor/me/sharing", "/donor/me/gifts", "/donor/me/recurring", "/donor/me/payment-method", "/donor/me/wallet"} {
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("GET %s anonymously: got %d, want 401", path, response.Code)
		}
	}
}

func TestFailuresAreLoggedWithoutPrivateDetail(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	failure := errors.New("donor@example.org could not be read")
	server := NewServer("", Services{Dashboard: &dashboardStub{err: failure}})
	r := httptest.NewRequest("GET", "/donor/me/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: donorCookie, Value: "session"})
	server.Handler.ServeHTTP(httptest.NewRecorder(), r)

	out := logs.String()
	if !strings.Contains(out, `"route":"GET /donor/me/dashboard"`) || !strings.Contains(out, `"status":503`) || !strings.Contains(out, `"cause":"*errors.errorString"`) {
		t.Fatalf("failure not logged with route, status, and class: %s", out)
	}
	if strings.Contains(out, "donor@example.org") {
		t.Fatal("log exposed private error text")
	}
}
