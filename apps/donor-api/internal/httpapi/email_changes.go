package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store"
)

type EmailChangeSource interface {
	Request(context.Context, string, string) error
	Confirm(context.Context, string) (string, error)
}

func installEmailChangeRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	for _, path := range []string{"/donor/me/email-change", "/donor/email-change/confirm"} {
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			if !guard.check(w, r, reply) {
				return
			}
			if services.EmailChange == nil || !services.EmailConfigured {
				reply(w, 503, map[string]string{"detail": "Email changes are temporarily unavailable"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var err error
			stage := "CURRENT"
			if r.URL.Path == "/donor/me/email-change" {
				var body struct {
					Email string `json:"email"`
				}
				if !decodeBody(w, r, &body) || len(body.Email) > 320 {
					reply(w, 422, map[string]string{"detail": "Enter a valid email address"})
					return
				}
				err = services.EmailChange.Request(ctx, sessionToken(r), body.Email)
			} else {
				var body struct {
					Token string `json:"token"`
				}
				if !decodeBody(w, r, &body) || len(body.Token) < 32 || len(body.Token) > 128 {
					reply(w, 422, map[string]string{"detail": "Invalid confirmation link"})
					return
				}
				stage, err = services.EmailChange.Confirm(ctx, body.Token)
			}
			switch {
			case errors.Is(err, store.ErrDenied):
				reply(w, 401, map[string]string{"detail": "Sign in again or request a fresh confirmation link."})
			case errors.Is(err, identity.ErrEmail):
				reply(w, 422, map[string]string{"detail": "Enter a valid email address"})
			case errors.Is(err, store.ErrEmailChangeConflict):
				reply(w, 409, map[string]string{"detail": "This address cannot replace the account email. Existing accounts are never merged automatically."})
			case err != nil:
				reply(w, 503, map[string]string{"detail": "The change was not confirmed. Please retry."})
			default:
				reply(w, 200, map[string]string{"stage": stage})
			}
		})
	}
}
