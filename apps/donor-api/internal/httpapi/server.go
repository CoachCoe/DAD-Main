// Package httpapi owns the independent donor HTTP transport.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"impactgraph/donor-api/internal/store"
)

type ContentSource interface {
	PublicContent(context.Context) (map[string]interface{}, error)
}
type DashboardSource interface {
	DonorDashboard(context.Context, string) (map[string]interface{}, error)
}

// NewServer keeps the incomplete rewrite off the existing Python service port.
// Readiness must remain unavailable until the complete donor path is implemented.
func NewServer(address string, content ContentSource, dashboard DashboardSource) *http.Server {
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
	mux.HandleFunc("GET /donor/public/content", func(w http.ResponseWriter, r *http.Request) {
		if content == nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		body, err := content.PublicContent(ctx)
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /donor/me/dashboard", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("dad_donor_session")
		if err != nil || cookie.Value == "" {
			reply(w, 401, map[string]string{"detail": "Sign in required"})
			return
		}
		if dashboard == nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		body, err := dashboard.DonorDashboard(ctx, cookie.Value)
		if errors.Is(err, store.ErrDenied) {
			reply(w, 401, map[string]string{"detail": "Sign in required"})
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		reply(w, 200, body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { reply(w, 404, map[string]string{"detail": "Not Found"}) })
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
}
