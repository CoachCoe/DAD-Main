// Package httpapi owns the independent donor HTTP transport.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

// NewServer keeps the incomplete rewrite off the existing Python service port.
// Readiness must remain unavailable until the complete donor path is implemented.
func NewServer(address string) *http.Server {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, body interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 503, map[string]string{"detail": "Go donor rewrite is not ready for traffic"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { reply(w, 404, map[string]string{"detail": "Not Found"}) })
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
}
