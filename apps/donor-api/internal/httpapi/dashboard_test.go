package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dad/donor-api/internal/store"
)

type dashboardStub struct {
	err   error
	token string
	calls int
}

func (s *dashboardStub) DonorDashboard(ctx context.Context, token string) (store.DonorDashboard, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded database access")
	}
	s.calls++
	s.token = token
	return store.DonorDashboard{Mode: "test", State: "preparing"}, s.err
}

func TestDashboardAuthenticationAndPrivateErrors(t *testing.T) {
	for _, tc := range []struct {
		token  string
		err    error
		status int
	}{
		{"", nil, 401}, {"existing-session", nil, 200}, {"invalid", store.ErrDenied, 401}, {"existing-session", errors.New("private database details"), 503},
	} {
		stub := &dashboardStub{err: tc.err}
		server := NewServer("", Services{Dashboard: stub})
		req := httptest.NewRequest("GET", "/donor/me/dashboard", nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: "dad_donor_session", Value: tc.token})
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, req)
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("session %q error %v: got %d %s, want %d uncached", tc.token, tc.err, response.Code, response.Body, tc.status)
		}
		if strings.Contains(response.Body.String(), "private database") {
			t.Fatal("database error leaked")
		}
		if tc.token == "" && stub.calls != 0 {
			t.Fatalf("anonymous request reached the database %d times", stub.calls)
		}
		if stub.token != tc.token {
			t.Fatalf("store saw session %q, want %q", stub.token, tc.token)
		}
	}
}
