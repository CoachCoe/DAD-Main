package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestFoundationCannotAdvertiseReadiness(t *testing.T) {
	server := NewServer("127.0.0.1:8002", nil, nil)
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
