package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/store"
)

var appSession = strings.Repeat("s", 43)

// sessionDonor accepts only appSession and records every session it is shown.
type sessionDonor struct {
	DonorSource
	sessions []string
}

func (s *sessionDonor) check(token string) error {
	s.sessions = append(s.sessions, token)
	if token != appSession {
		return store.ErrDenied
	}
	return nil
}

func (s *sessionDonor) Profile(_ context.Context, token string) (store.Profile, error) {
	return store.Profile{Email: "donor@example.org"}, s.check(token)
}

func (s *sessionDonor) SavePreferences(_ context.Context, token string, _ store.Preferences) (store.Profile, error) {
	return store.Profile{Email: "donor@example.org"}, s.check(token)
}

func (s *sessionDonor) SignOutEverywhere(_ context.Context, token string) error {
	return s.check(token)
}

func appRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-DAD-Client", "native")
	r.Header.Set("Authorization", "Bearer "+appSession)
	return r
}

const preferences = `{"version":0,"referral_emails":true,"marketing_emails":false}`

func TestDonorSessionSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		want   string
	}{
		{"app bearer", func(*http.Request) {}, appSession},
		{"lower-case scheme", func(r *http.Request) { r.Header.Set("Authorization", "bearer "+appSession) }, appSession},
		{"other scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+appSession) }, ""},
		{"short token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer short") }, ""},
		{"no client marker", func(r *http.Request) { r.Header.Del("X-DAD-Client") }, ""},
		{"browser origin", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000") }, ""},
		{"browser cookie wins", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: donorCookie, Value: "cookie-session"}) }, "cookie-session"},
	} {
		r := appRequest("GET", "/donor/me/profile", "")
		tc.mutate(r)
		if got := donorSession(r); got != tc.want {
			t.Errorf("%s: got session %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAppBearerSessionWorksWithoutOrigin(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/donor/me/profile", ""},
		{"POST", "/donor/me/preferences", preferences},
		{"POST", "/donor/logout-all", ""},
	} {
		donor := &sessionDonor{}
		response := httptest.NewRecorder()
		NewServer("", Services{Donor: donor}).Handler.ServeHTTP(response, appRequest(tc.method, tc.path, tc.body))
		if response.Code != http.StatusOK || len(donor.sessions) != 1 || donor.sessions[0] != appSession {
			t.Errorf("%s %s: got %d with sessions %q, want 200 with the bearer session", tc.method, tc.path, response.Code, donor.sessions)
		}
		if cookies := response.Result().Cookies(); len(cookies) != 0 {
			t.Errorf("%s %s: app response set cookies %v", tc.method, tc.path, cookies)
		}
	}
}

func TestBrowserSignalsDisqualifyBearerRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		// A cookie makes the request a browser request: Origin is required.
		{"cookie and bearer", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: donorCookie, Value: appSession}) }, http.StatusForbidden},
		// A page elsewhere cannot add the marker without a preflight, but a request
		// that claims it alongside a foreign Origin is still a browser request.
		{"cross-origin marker", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.org") }, http.StatusForbidden},
		{"bearer without marker", func(r *http.Request) { r.Header.Del("X-DAD-Client") }, http.StatusForbidden},
		{"same-origin browser with bearer only", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000") }, http.StatusUnauthorized},
	} {
		donor := &sessionDonor{}
		r := appRequest("POST", "/donor/me/preferences", preferences)
		tc.mutate(r)
		response := httptest.NewRecorder()
		NewServer("", Services{Donor: donor}).Handler.ServeHTTP(response, r)
		if response.Code != tc.status || len(donor.sessions) != 0 {
			t.Errorf("%s: got %d after %d store calls, want %d before any", tc.name, response.Code, len(donor.sessions), tc.status)
		}
	}
}

func TestRevokedOrExpiredBearerIsUnauthorized(t *testing.T) {
	donor := &sessionDonor{}
	server := NewServer("", Services{Donor: donor})
	for _, method := range []string{"GET", "POST"} {
		path, body := "/donor/me/profile", ""
		if method == "POST" {
			path, body = "/donor/me/preferences", preferences
		}
		r := appRequest(method, path, body)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 43))
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a session the store denies: got %d, want 401", method, path, response.Code)
		}
	}
}

func TestAppRequestsAreRateLimited(t *testing.T) {
	donor := &sessionDonor{}
	server := NewServer("", Services{Donor: donor})
	for range accountLimit.limit {
		server.Handler.ServeHTTP(httptest.NewRecorder(), appRequest("POST", "/donor/me/preferences", preferences))
	}
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, appRequest("POST", "/donor/me/preferences", preferences))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d with Retry-After %q, want 429 with a delay", response.Code, response.Header().Get("Retry-After"))
	}
	if len(donor.sessions) != accountLimit.limit {
		t.Fatalf("store saw %d requests, want %d", len(donor.sessions), accountLimit.limit)
	}
}

func TestClaimReturnsSessionTokenOnlyToTheApp(t *testing.T) {
	proof := `{"token":"` + strings.Repeat("p", 43) + `"}`
	for _, tc := range []struct {
		name        string
		request     func() *http.Request
		status      int
		bodyToken   bool
		cookieToken bool
	}{
		{"app", func() *http.Request {
			r := appRequest("POST", "/donor/claim", proof)
			r.Header.Del("Authorization")
			return r
		}, http.StatusOK, true, false},
		{"browser", func() *http.Request {
			r := httptest.NewRequest("POST", "/donor/claim", strings.NewReader(proof))
			r.Header.Set("Origin", "http://localhost:3000")
			return r
		}, http.StatusOK, false, true},
		{"browser claiming the marker", func() *http.Request {
			r := httptest.NewRequest("POST", "/donor/claim", strings.NewReader(proof))
			r.Header.Set("Origin", "http://localhost:3000")
			r.Header.Set("X-DAD-Client", "native")
			return r
		}, http.StatusOK, false, true},
		{"marker with cookie and no origin", func() *http.Request {
			r := appRequest("POST", "/donor/claim", proof)
			r.AddCookie(&http.Cookie{Name: donorCookie, Value: "old"})
			return r
		}, http.StatusForbidden, false, false},
	} {
		response := httptest.NewRecorder()
		NewServer("", Services{Account: &accountStub{}}).Handler.ServeHTTP(response, tc.request())
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		_, bodyToken := body["session_token"]
		cookieToken := strings.Contains(response.Header().Get("Set-Cookie"), "new-secret-session")
		if response.Code != tc.status || bodyToken != tc.bodyToken || cookieToken != tc.cookieToken {
			t.Errorf("%s: got %d, token in body %v, in cookie %v; want %d, %v, %v",
				tc.name, response.Code, bodyToken, cookieToken, tc.status, tc.bodyToken, tc.cookieToken)
		}
		if tc.bodyToken && (body["session_token"] != "new-secret-session" || body["expires_in"] != float64(donorSessionAge)) {
			t.Errorf("%s: native session body %v", tc.name, body)
		}
	}
}

func TestAppClaimRevokesThePresentedSession(t *testing.T) {
	stub := &accountStub{}
	r := appRequest("POST", "/donor/claim", `{"token":"`+strings.Repeat("p", 43)+`"}`)
	NewServer("", Services{Account: stub}).Handler.ServeHTTP(httptest.NewRecorder(), r)
	if stub.old != appSession {
		t.Fatalf("claim revoked %q, want the presented bearer session", stub.old)
	}
}

func TestAppLogoutRevokesBearerWithoutCookies(t *testing.T) {
	stub := &accountStub{}
	response := httptest.NewRecorder()
	NewServer("", Services{Account: stub}).Handler.ServeHTTP(response, appRequest("POST", "/donor/logout", ""))
	if response.Code != http.StatusOK || stub.old != appSession || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("got %d revoking %q with Set-Cookie %q, want 200 revoking the bearer session and no cookie",
			response.Code, stub.old, response.Header().Get("Set-Cookie"))
	}
}

// The app may call donor account routes only; giving, referral capture,
// partner signup, and the back office stay on the web.
func TestOnlyDonorAccountRoutesAdmitTheApp(t *testing.T) {
	entries := map[string]bool{"POST /donor/login": true, "POST /donor/claim": true, "POST /donor/email-change/confirm": true, "POST /donor/logout": true, "POST /donor/logout-all": true}
	server := NewServer("", Services{})
	for _, route := range allRoutes() {
		if !strings.HasPrefix(route.pattern, "POST ") || route.auth == stripeAuth {
			continue
		}
		admitted := entries[route.pattern] || strings.HasPrefix(route.pattern, "POST /donor/me/")
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, appRequest("POST", routeRequest(route.pattern, "").URL.Path, "{}"))
		if got := response.Code != http.StatusForbidden; got != admitted {
			t.Errorf("%s from the app: got %d, want admitted=%v", route.pattern, response.Code, admitted)
		}
	}
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, appRequest("GET", "/donor/staff/me", ""))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("staff read with a bearer token: got %d, want 401", response.Code)
	}
}
