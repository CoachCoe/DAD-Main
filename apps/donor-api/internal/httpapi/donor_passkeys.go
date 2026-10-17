package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

type DonorPasskeySource interface {
	BeginSignIn(context.Context) (store.Ceremony, error)
	FinishSignIn(context.Context, string, []byte, store.Client) (store.Tokens, error)
	BeginRegister(context.Context, string) (store.Ceremony, error)
	FinishRegister(context.Context, string, string, []byte, string) (store.PasskeyList, error)
	ListPasskeys(context.Context, string) (store.PasskeyList, error)
	DeletePasskey(context.Context, string, pgtype.UUID) (store.PasskeyList, error)
}

const donorPasskeyUnavailable = "Passkey sign-in is temporarily unavailable"

func (a *api) donorPasskeys() service {
	return service{a.DonorPasskeys != nil, donorPasskeyUnavailable, readTimeout, donorPasskeyStatus}
}

func donorPasskeyStatus(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrPasskeyDenied):
		return http.StatusUnauthorized, "Passkey or ceremony invalid or expired"
	case errors.Is(err, store.ErrDenied):
		return http.StatusUnauthorized, "Sign in required"
	default:
		return http.StatusServiceUnavailable, donorPasskeyUnavailable
	}
}

func (a *api) beginDonorPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	a.donorPasskeys().serve(w, r, func(ctx context.Context) (any, error) {
		return a.DonorPasskeys.BeginSignIn(ctx)
	})
}

// donorPasskeySignInBody answers a sign-in ceremony. Like a claim, the app
// may describe its device; a browser's description is ignored.
type donorPasskeySignInBody struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Device     json.RawMessage `json:"device_name"`
	Platform   json.RawMessage `json:"platform"`
}

func (b donorPasskeySignInBody) client() (store.Client, bool) {
	device, deviceOK := optional(b.Device, "")
	platform, platformOK := optional(b.Platform, "")
	return store.Client{App: true, Device: device, Platform: platform}, deviceOK && utf8.RuneCountInString(device) <= store.MaxDeviceName &&
		platformOK && (platform == "" || platform == "IOS" || platform == "ANDROID")
}

func (b donorPasskeySignInBody) valid() bool {
	_, ok := b.client()
	return ok && passkeyBody{b.Ceremony, b.Credential}.valid()
}

func (a *api) finishDonorPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeValid[donorPasskeySignInBody](w, r)
	if !ok {
		return
	}
	client := store.Client{}
	if native(r) {
		client, _ = body.client()
	}
	a.donorPasskeys().serve(w, r, func(ctx context.Context) (any, error) {
		tokens, err := a.DonorPasskeys.FinishSignIn(ctx, body.Ceremony, body.Credential, client)
		if err != nil {
			return nil, err
		}
		if client.App {
			return newNativeSession(tokens), nil
		}
		a.setCookie(w, donorCookie, tokens.Access, int(tokens.AccessTTL.Seconds()), http.SameSiteLaxMode)
		return map[string]bool{"signed_in": true}, nil
	})
}

func (a *api) beginDonorPasskeyRegister(w http.ResponseWriter, r *http.Request, session string) {
	a.donorPasskeys().serve(w, r, func(ctx context.Context) (any, error) {
		return a.DonorPasskeys.BeginRegister(ctx, session)
	})
}

type registerPasskeyBody struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Name       *string         `json:"name"`
}

func (b registerPasskeyBody) valid() bool {
	name := ""
	if b.Name != nil {
		name = *b.Name
	}
	return validToken(b.Ceremony) &&
		len(b.Credential) > 0 &&
		len(b.Credential) <= maxCredentialBytes &&
		store.ValidPasskeyName(name)
}

func (a *api) finishDonorPasskeyRegister(w http.ResponseWriter, r *http.Request, session string) {
	body, ok := decodeValid[registerPasskeyBody](w, r)
	if !ok {
		return
	}
	name := ""
	if body.Name != nil {
		name = *body.Name
	}
	a.donorPasskeys().serve(w, r, func(ctx context.Context) (any, error) {
		return a.DonorPasskeys.FinishRegister(ctx, session, body.Ceremony, body.Credential, name)
	})
}

func (a *api) listDonorPasskeys(w http.ResponseWriter, r *http.Request, session string) {
	a.donorPasskeys().serve(w, r, func(ctx context.Context) (any, error) {
		return a.DonorPasskeys.ListPasskeys(ctx, session)
	})
}

func (a *api) deleteDonorPasskey(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "passkey_id")
	if !ok {
		return
	}
	a.donorPasskeys().missing("Passkey not found").serve(w, r, func(ctx context.Context) (any, error) {
		return a.DonorPasskeys.DeletePasskey(ctx, session, id)
	})
}
