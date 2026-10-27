package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"dad/donor-api/internal/push"
	"dad/donor-api/internal/store"
)

type PushSource interface {
	RegisterDevice(context.Context, string, store.PushDevice) (store.PushRegistration, error)
	UnregisterDevice(context.Context, string) (store.PushRegistration, error)
}

func (a *api) push() service {
	return service{a.Push != nil, donorUnavailable, readTimeout, donorStatus}
}

func (a *api) registerPushDevice(w http.ResponseWriter, r *http.Request, session string) {
	registerPush(w, r, session, a.Push, a.push())
}

func (a *api) partnerPush() service {
	return service{a.PartnerPush != nil, "The partner portal is temporarily unavailable", readTimeout, partnerStatusCode}
}

func (a *api) registerPartnerPushDevice(w http.ResponseWriter, r *http.Request, session string) {
	registerPush(w, r, session, a.PartnerPush, a.partnerPush())
}

func (a *api) unregisterPartnerPushDevice(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPush().serve(w, r, func(ctx context.Context) (any, error) { return a.PartnerPush.UnregisterDevice(ctx, session) })
}

func registerPush(w http.ResponseWriter, r *http.Request, session string, source PushSource, svc service) {
	var body struct {
		Platform    *string         `json:"platform"`
		Token       *string         `json:"token"`
		Environment json.RawMessage `json:"environment"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	environment, ok := optional(body.Environment, "")
	if !ok || body.Platform == nil || (*body.Platform != push.IOS && *body.Platform != push.Android) || body.Token == nil {
		invalidFields(w, r)
		return
	}
	device := store.PushDevice{Platform: *body.Platform, Token: *body.Token, Environment: environment}
	svc.serve(w, r, func(ctx context.Context) (any, error) { return source.RegisterDevice(ctx, session, device) })
}

func (a *api) unregisterPushDevice(w http.ResponseWriter, r *http.Request, session string) {
	a.push().serve(w, r, func(ctx context.Context) (any, error) { return a.Push.UnregisterDevice(ctx, session) })
}

func (a *api) conversions(w http.ResponseWriter, r *http.Request, session string) {
	var since *time.Time
	if value := r.URL.Query().Get("since"); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			invalidFields(w, r)
			return
		}
		since = &parsed
	}
	a.donor().serve(w, r, func(ctx context.Context) (any, error) { return a.Donor.Conversions(ctx, session, since) })
}
