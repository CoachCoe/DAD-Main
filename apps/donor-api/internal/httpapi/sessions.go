package httpapi

import (
	"context"
	"net/http"
)

func (a *api) sessions(w http.ResponseWriter, r *http.Request, session string) {
	a.donor().serve(w, r, func(ctx context.Context) (any, error) { return a.Donor.Sessions(ctx, session) })
}

func (a *api) signOutSession(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "session_id")
	if !ok {
		return
	}
	a.donor().missing("Session not found").serve(w, r, func(ctx context.Context) (any, error) { return a.Donor.SignOutSession(ctx, session, id) })
}

func (a *api) signOutOthers(w http.ResponseWriter, r *http.Request, session string) {
	a.donor().serve(w, r, func(ctx context.Context) (any, error) { return a.Donor.SignOutOthers(ctx, session) })
}
