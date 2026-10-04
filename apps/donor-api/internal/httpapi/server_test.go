package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestUnconfiguredServerCannotAdvertiseReadiness(t *testing.T) {
	server := NewServer("127.0.0.1:8002", Services{})
	for _, test := range []struct {
		path   string
		status int
	}{{"/health/live", 200}, {"/health/ready", 503}, {"/donor/checkouts", 404}} {
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
		if failure != nil && response.Body.String() == failure.Error() {
			t.Fatal("database details exposed")
		}
	}
}
