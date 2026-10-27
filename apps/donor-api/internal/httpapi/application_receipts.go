package httpapi

import (
	"context"
	"net/http"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

const applicationCookie = "dad_application_receipt"

type ApplicationReceiptSource interface {
	ApplyWithReceipt(context.Context, pgtype.UUID, store.PartnerApplicationInput, string) error
	ApplicationStatus(context.Context, string) (store.ApplicationStatus, error)
}

type applicationReceiptBody struct {
	Application store.PartnerApplicationInput `json:"application"`
	Receipt     string                        `json:"receipt"`
}

func (a *api) applicationReceipts() service {
	return service{a.ApplicationReceipts != nil, "Partner applications are temporarily unavailable", readTimeout, partnerStatusCode}
}

func (a *api) applyWithReceipt(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body applicationReceiptBody
	if !decodeJSON(w, r, &body) {
		return
	}
	a.applicationReceipts().serve(w, r, func(ctx context.Context) (any, error) {
		if err := a.ApplicationReceipts.ApplyWithReceipt(ctx, key, body.Application, body.Receipt); err != nil {
			return nil, err
		}
		if !native(r) {
			a.setCookie(w, applicationCookie, body.Receipt, 30*24*60*60, http.SameSiteLaxMode)
		}
		return map[string]bool{"received": true}, nil
	})
}

func applicationSession(r *http.Request) string {
	if native(r) {
		return partnerSession(r)
	}
	return cookieValue(r, applicationCookie)
}

func (a *api) applicationStatus(w http.ResponseWriter, r *http.Request, receipt string) {
	a.applicationReceipts().
		serve(w, r, func(ctx context.Context) (any, error) { return a.ApplicationReceipts.ApplicationStatus(ctx, receipt) })
}
