package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store"
)

type AccountSource interface {
	ClaimAccount(context.Context, string, string) (string, error)
	SignOut(context.Context, string) error
	QueueLogin(context.Context, string) error
}

func installAccountRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	mux.HandleFunc("POST /donor/login", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body struct {
			Email *string `json:"email"`
		}
		if !decodeBody(w, r, &body) || body.Email == nil || utf8.RuneCountInString(*body.Email) > 320 {
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
			return
		}
		if !services.EmailConfigured {
			reply(w, 503, map[string]string{"detail": "Account email is not configured yet"})
			return
		}
		if services.Account == nil {
			reply(w, 503, map[string]string{"detail": "Account sign-in is temporarily unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := services.Account.QueueLogin(ctx, *body.Email)
		if errors.Is(err, identity.ErrEmail) {
			reply(w, 422, map[string]string{"detail": "Enter a valid email address"})
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Account sign-in is temporarily unavailable"})
			return
		}
		reply(w, 202, map[string]string{"message": "If this email matches a donor account, a sign-in link will arrive shortly."})
	})
	mux.HandleFunc("POST /donor/claim", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body struct {
			Token *string `json:"token"`
		}
		if !decodeBody(w, r, &body) || body.Token == nil || utf8.RuneCountInString(*body.Token) < 32 || utf8.RuneCountInString(*body.Token) > 128 {
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
			return
		}
		if services.Account == nil {
			reply(w, 503, map[string]string{"detail": "Account sign-in is temporarily unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		token, err := services.Account.ClaimAccount(ctx, *body.Token, sessionToken(r))
		if errors.Is(err, store.ErrDenied) {
			reply(w, 401, map[string]string{"detail": "This link is invalid or expired. Request a new link."})
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Account sign-in is temporarily unavailable"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "dad_donor_session", Value: token, MaxAge: 30 * 86400, HttpOnly: true, Secure: strings.HasPrefix(guard.origin, "https:"), SameSite: http.SameSiteLaxMode, Path: "/"})
		reply(w, 200, map[string]bool{"claimed": true})
	})
	mux.HandleFunc("POST /donor/logout", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		if services.Account == nil {
			reply(w, 503, map[string]string{"detail": "Account sign-in is temporarily unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := services.Account.SignOut(ctx, sessionToken(r)); err != nil {
			reply(w, 503, map[string]string{"detail": "Sign-out is temporarily unavailable"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "dad_donor_session", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(guard.origin, "https:"), SameSite: http.SameSiteLaxMode, Path: "/"})
		reply(w, 200, map[string]bool{"signed_out": true})
	})
}
