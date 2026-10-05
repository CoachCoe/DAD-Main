package httpapi

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store"
)

type AccountSource interface {
	ClaimAccount(context.Context, string, string) (string, error)
	SignOut(context.Context, string) error
	QueueLogin(context.Context, string) error
}

const (
	donorSessionAge = 30 * 24 * 60 * 60
	maxEmailRunes   = 320
)

func validProof(token string) bool {
	n := utf8.RuneCountInString(token)
	return n >= 32 && n <= 128
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Account sign-in is temporarily unavailable"
	var body struct {
		Email *string `json:"email"`
	}
	if !decodeJSON(w, r, &body) || body.Email == nil || utf8.RuneCountInString(*body.Email) > maxEmailRunes {
		invalidFields(w, r)
		return
	}
	if !a.EmailConfigured {
		writeError(w, r, http.StatusServiceUnavailable, "Account email is not configured yet", nil)
		return
	}
	if a.Account == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	switch err := a.Account.QueueLogin(ctx, *body.Email); {
	case errors.Is(err, identity.ErrEmail):
		writeError(w, r, http.StatusUnprocessableEntity, "Enter a valid email address", nil)
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, unavailable, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "If this email matches a donor account, a sign-in link will arrive shortly."})
	}
}

func (a *api) claim(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Account sign-in is temporarily unavailable"
	var body struct {
		Token *string `json:"token"`
	}
	if !decodeJSON(w, r, &body) || body.Token == nil || !validProof(*body.Token) {
		invalidFields(w, r)
		return
	}
	if a.Account == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	token, err := a.Account.ClaimAccount(ctx, *body.Token, cookieValue(r, donorCookie))
	switch {
	case errors.Is(err, store.ErrDenied):
		writeError(w, r, http.StatusUnauthorized, "This link is invalid or expired. Request a new link.", nil)
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, unavailable, err)
	default:
		a.setCookie(w, donorCookie, token, donorSessionAge, http.SameSiteLaxMode)
		writeJSON(w, http.StatusOK, map[string]bool{"claimed": true})
	}
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if a.Account == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Account sign-in is temporarily unavailable", nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	if err := a.Account.SignOut(ctx, cookieValue(r, donorCookie)); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "Sign-out is temporarily unavailable", err)
		return
	}
	a.setCookie(w, donorCookie, "", -1, http.SameSiteLaxMode)
	writeJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}
