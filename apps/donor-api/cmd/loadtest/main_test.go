package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadSpreadsRequestsAndCountsFailures(t *testing.T) {
	var good, bad, cookies atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "dad_donor_session=synthetic" {
			cookies.Add(1)
		}
		switch r.URL.Path {
		case "/ok":
			good.Add(1)
		case "/large":
			bad.Add(1)
			_, _ = w.Write([]byte(strings.Repeat("x", maxBodyBytes+1)))
		default:
			bad.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	r := load(context.Background(), config{
		targets:     []string{server.URL + "/ok", server.URL + "/unavailable", server.URL + "/large"},
		duration:    200 * time.Millisecond,
		concurrency: 4,
		cookie:      "dad_donor_session=synthetic",
	})
	if r.Requests == 0 || r.Requests != good.Load()+bad.Load() {
		t.Fatalf("reported %d requests, server saw %d", r.Requests, good.Load()+bad.Load())
	}
	if r.Failed != bad.Load() {
		t.Fatalf("reported %d failures, want %d (non-200 and oversized bodies)", r.Failed, bad.Load())
	}
	if good.Load() == 0 || bad.Load() < good.Load() {
		t.Fatalf("requests were not spread round-robin: %d good, %d bad", good.Load(), bad.Load())
	}
	if cookies.Load() != r.Requests {
		t.Fatal("authenticated load did not send the session cookie on every request")
	}
	if r.P50ms > r.P95ms || r.P95ms > r.P99ms {
		t.Fatalf("percentiles out of order: %+v", r)
	}
}

func TestParseTargetsRejectsUnsafeURLs(t *testing.T) {
	if targets, err := parseTargets("http://127.0.0.1:8002/a, https://dad.example/b"); err != nil || len(targets) != 2 {
		t.Fatalf("valid targets rejected: %v %v", targets, err)
	}
	for _, invalid := range []string{"", "ftp://host/x", "http://user:pass@host/x", "/relative"} {
		if _, err := parseTargets(invalid); err == nil {
			t.Errorf("%q accepted", invalid)
		}
	}
}
