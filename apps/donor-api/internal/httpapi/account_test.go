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

type accountStub struct {
	err               error
	proof, old, email string
}

func (s *accountStub) ClaimAccount(_ context.Context, proof, old string) (string, error) {
	s.proof, s.old = proof, old
	return "new-secret-session", s.err
}
func (s *accountStub) SignOut(_ context.Context, token string) error { s.old = token; return s.err }
func (s *accountStub) QueueLogin(_ context.Context, email string) error {
	s.email = email
	return s.err
}
func TestClaimSetsCookieOnlyAfterSuccess(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {store.ErrDenied, 401}, {errors.New("private failure"), 503}} {
		stub := &accountStub{err: tc.err}
		server := NewServer("", Services{Account: stub, Origin: "https://dad.example.org"})
		proof := strings.Repeat("a", 43)
		req := httptest.NewRequest("POST", "/donor/claim", strings.NewReader(`{"token":"`+proof+`"}`))
		req.Header.Set("Origin", "https://dad.example.org")
		req.AddCookie(&http.Cookie{Name: "dad_donor_session", Value: "old-session"})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, req)
		if response.Code != tc.status || stub.proof != proof || stub.old != "old-session" {
			t.Fatal("claim contract changed")
		}
		if strings.Contains(response.Body.String(), proof) || strings.Contains(response.Body.String(), "private") {
			t.Fatal("claim leaked credentials")
		}
		cookies := response.Result().Cookies()
		if tc.err != nil {
			if len(cookies) > 0 {
				t.Fatal("failed claim set a session")
			}
			continue
		}
		if len(cookies) != 1 || cookies[0].Name != "dad_donor_session" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != 30*86400 || cookies[0].Path != "/" {
			t.Fatal("unsafe session cookie")
		}
	}
}
func TestLoginIsNonEnumeratingAndConfigurationGated(t *testing.T) {
	for _, configured := range []bool{false, true} {
		stub := &accountStub{}
		server := NewServer("", Services{Account: stub, EmailConfigured: configured})
		req := httptest.NewRequest("POST", "/donor/login", strings.NewReader(`{"email":"donor@example.org"}`))
		req.Header.Set("Origin", "http://localhost:3000")
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, req)
		if configured {
			if response.Code != 202 || strings.Contains(response.Body.String(), "donor@example.org") {
				t.Fatal("login enumerates account")
			}
		} else if response.Code != 503 || stub.email != "" {
			t.Fatal("unconfigured login queued")
		}
	}
}
