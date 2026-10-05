package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnconfiguredServerCannotAdvertiseReadiness(t *testing.T) {
	server := NewServer("127.0.0.1:8002", Services{})
	for _, test := range []struct {
		path   string
		status int
	}{{"/health/live", 200}, {"/health/ready", 503}, {"/donor/checkouts", 405}, {"/donor/unknown", 404}} {
		request := httptest.NewRequest("GET", test.path, nil)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s: status %d", test.path, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("health and private errors must not be cached")
		}
	}
}

func TestReadinessChecksDatabaseWithinDeadline(t *testing.T) {
	for _, failure := range []error{nil, errors.New("private database failure")} {
		called := false
		server := NewServer("", Services{Ready: func(ctx context.Context) error {
			called = true
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("readiness lacks a deadline")
			}
			return failure
		}})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/health/ready", nil))
		want := 200
		if failure != nil {
			want = 503
		}
		if !called || response.Code != want {
			t.Fatalf("called=%v status=%d", called, response.Code)
		}
		if failure != nil && strings.Contains(response.Body.String(), failure.Error()) {
			t.Fatal("database details exposed")
		}
	}
}

func TestUnmatchedRoutesAnswerInJSONAndKeepAllow(t *testing.T) {
	server := NewServer("", Services{})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest("DELETE", "/donor/checkouts", nil))
	if response.Code != 405 || response.Header().Get("Allow") != "POST" || response.Header().Get("Content-Type") != "application/json" || !strings.Contains(response.Body.String(), `"detail":"Method Not Allowed"`) {
		t.Fatalf("%d %v %s", response.Code, response.Header(), response.Body.String())
	}
}

func TestSecurityHeadersApplyToEveryResponse(t *testing.T) {
	server := NewServer("", Services{})
	for _, path := range []string{"/health/live", "/donor/unknown", "/donor/me/profile"} {
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		h := response.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s missing security headers: %v", path, h)
		}
	}
}
