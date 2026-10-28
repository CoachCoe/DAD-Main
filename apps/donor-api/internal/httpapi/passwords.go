package httpapi

import (
	"context"
	"dad/donor-api/internal/geo"
	"dad/donor-api/internal/store"
	"errors"
	"net/http"
)

type PasswordSource interface {
	Signup(context.Context, store.SignupInput) error
	Set(context.Context, string, string, string) error
	SignIn(context.Context, string, string, store.Client) (store.Tokens, error)
}

func (a *api) passwords() service {
	return service{a.Passwords != nil, "Account access is temporarily unavailable", readTimeout, passwordStatus}
}
func passwordStatus(err error) (int, string) {
	if errors.Is(err, store.ErrPassword) {
		return http.StatusUnprocessableEntity, err.Error()
	}
	if errors.Is(err, store.ErrStaffInput) || errors.Is(err, geo.ErrZip) {
		return http.StatusUnprocessableEntity, "Enter your name, city and a valid ZIP code"
	}
	if errors.Is(err, store.ErrSignInCode) || errors.Is(err, store.ErrDenied) {
		return http.StatusUnauthorized, "The email or password is incorrect"
	}
	return accountStatus(err)
}
func (a *api) signup(w http.ResponseWriter, r *http.Request) {
	var body store.SignupInput
	if !decodeJSON(w, r, &body) {
		return
	}
	if !a.EmailConfigured {
		emailOff.write(w)
		return
	}
	a.passwords().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]bool{"sent": true}, a.Passwords.Signup(ctx, body)
	})
}
func (a *api) passwordSignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	client, ok := signInClient(r, nil, nil)
	if !ok {
		invalidFields(w, r)
		return
	}
	a.passwords().serve(w, r, func(ctx context.Context) (any, error) {
		tokens, err := a.Passwords.SignIn(ctx, body.Email, body.Password, client)
		if err != nil {
			return nil, err
		}
		return a.sessionResponse(w, client, tokens), nil
	})
}
func (a *api) setPassword(w http.ResponseWriter, r *http.Request, token string) {
	var body struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.passwords().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]bool{"saved": true}, a.Passwords.Set(ctx, token, body.Current, body.Password)
	})
}
