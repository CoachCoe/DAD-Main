package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store"
)

type AccountSource interface {
	ClaimAccount(context.Context, string, string) (string, error)
	SignOut(context.Context, string) error
	QueueLogin(ctx context.Context, email, next string) error
}

const (
	donorSessionAge = 30 * 24 * 60 * 60
	maxEmailRunes   = 320
)

// nativeSession hands the donor app its session; browsers get the same
// secret only as an HttpOnly cookie.
type nativeSession struct {
	Claimed      bool   `json:"claimed"`
	SessionToken string `json:"session_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (a *api) account() service {
	return service{a.Account != nil, "Account sign-in is temporarily unavailable", readTimeout, accountStatus}
}

func accountStatus(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrEmail):
		return http.StatusUnprocessableEntity, "Enter a valid email address"
	case errors.Is(err, store.ErrDenied):
		return http.StatusUnauthorized, "This link is invalid or expired. Request a new link."
	default:
		return http.StatusServiceUnavailable, "Account sign-in is temporarily unavailable"
	}
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email *string         `json:"email"`
		Next  json.RawMessage `json:"next"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	next, ok := optional(body.Next, "")
	if !ok || (next != "" && !store.ValidReturnPath(next)) || body.Email == nil || utf8.RuneCountInString(*body.Email) > maxEmailRunes {
		invalidFields(w, r)
		return
	}
	if !a.EmailConfigured {
		emailOff.write(w)
		return
	}
	if !a.account().available(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	if err := a.Account.QueueLogin(ctx, *body.Email, next); err != nil {
		status, detail := accountStatus(err)
		writeError(w, r, status, detail, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "If this email matches a donor account, a sign-in link will arrive shortly."})
}

// claim exchanges an emailed proof for a session, revoking the session the
// caller presented, if any.
func (a *api) claim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token *string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Token == nil || !validToken(*body.Token) {
		invalidFields(w, r)
		return
	}
	if !a.account().available(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	token, err := a.Account.ClaimAccount(ctx, *body.Token, donorSession(r))
	if err != nil {
		status, detail := accountStatus(err)
		writeError(w, r, status, detail, err)
		return
	}
	if native(r) {
		writeJSON(w, http.StatusOK, nativeSession{Claimed: true, SessionToken: token, ExpiresIn: donorSessionAge})
		return
	}
	a.setCookie(w, donorCookie, token, donorSessionAge, http.SameSiteLaxMode)
	writeJSON(w, http.StatusOK, map[string]bool{"claimed": true})
}

// logout succeeds even without a valid session, so a client can always
// discard its credential.
func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if !a.account().available(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	if err := a.Account.SignOut(ctx, donorSession(r)); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "Sign-out is temporarily unavailable", err)
		return
	}
	a.clearDonorCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

func (a *api) clearDonorCookie(w http.ResponseWriter, r *http.Request) {
	if !native(r) {
		a.setCookie(w, donorCookie, "", -1, http.SameSiteLaxMode)
	}
}
