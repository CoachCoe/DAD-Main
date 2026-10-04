package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/store"
)

type dashboardStub struct {
	err   error
	token string
	calls int
}

func (s *dashboardStub) DonorDashboard(ctx context.Context, token string) (map[string]interface{}, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded database access")
	}
	s.calls++
	s.token = token
	return map[string]interface{}{"mode": "test", "state": "preparing"}, s.err
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
		server := NewServer("", nil, stub)
		req := httptest.NewRequest("GET", "/donor/me/dashboard", nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: "dad_donor_session", Value: tc.token})
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, req)
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private database") {
			t.Fatal("database error leaked")
		}
		if tc.token == "" && stub.calls != 0 {
			t.Fatal("anonymous request reached database")
		}
		if stub.token != tc.token {
			t.Fatal("session not passed through")
		}
	}
}
