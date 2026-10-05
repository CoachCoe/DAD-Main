package httpapi

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store"
)

type EmailChangeSource interface {
	Request(context.Context, string, string) error
	Confirm(context.Context, string) (string, error)
}

func (a *api) emailChangeAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.EmailChange == nil || !a.EmailConfigured {
		writeError(w, r, http.StatusServiceUnavailable, "Email changes are temporarily unavailable", nil)
		return false
	}
	return true
}

func (a *api) requestEmailChange(w http.ResponseWriter, r *http.Request, session string) {
	if !a.emailChangeAvailable(w, r) {
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) || utf8.RuneCountInString(body.Email) > maxEmailRunes {
		writeError(w, r, http.StatusUnprocessableEntity, "Enter a valid email address", nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	err := a.EmailChange.Request(ctx, session, body.Email)
	respond(w, r, map[string]string{"stage": "CURRENT"}, err, emailChangeStatus)
}

func (a *api) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	if !a.emailChangeAvailable(w, r) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) || !validProof(body.Token) {
		writeError(w, r, http.StatusUnprocessableEntity, "Invalid confirmation link", nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	stage, err := a.EmailChange.Confirm(ctx, body.Token)
	respond(w, r, map[string]string{"stage": stage}, err, emailChangeStatus)
}

func emailChangeStatus(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrDenied):
		return http.StatusUnauthorized, "Sign in again or request a fresh confirmation link."
	case errors.Is(err, identity.ErrEmail):
		return http.StatusUnprocessableEntity, "Enter a valid email address"
	case errors.Is(err, store.ErrEmailChangeConflict):
		return http.StatusConflict, "This address cannot replace the account email. Existing accounts are never merged automatically."
	default:
		return http.StatusServiceUnavailable, "The change was not confirmed. Please retry."
	}
}
