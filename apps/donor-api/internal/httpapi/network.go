package httpapi

import (
	"context"
	"net/http"

	"dad/donor-api/internal/store"
)

// NetworkSource is the donor's referral-network read (PRD §6.5 I-2, §6.7).
type NetworkSource interface {
	Network(context.Context, string) (store.NetworkSummary, error)
}

// donorNetwork serves the signed-in donor's network card: active-network
// annualized giving, next milestone, and the opt-in invitee list.
func (a *api) donorNetwork(w http.ResponseWriter, r *http.Request, session string) {
	svc := service{a.Network != nil, donorUnavailable, readTimeout, donorStatus}
	svc.serve(w, r, func(ctx context.Context) (any, error) {
		return a.Network.Network(ctx, session)
	})
}
