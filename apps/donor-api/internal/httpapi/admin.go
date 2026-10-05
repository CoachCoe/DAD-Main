package httpapi

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/store/queries"
)

type AdminSource interface {
	SearchDonors(context.Context, string, string, int64) (map[string]any, error)
	DonorDetail(context.Context, string, pgtype.UUID, int64, int64) (map[string]any, error)
	AuditEvents(context.Context, string, int64) (map[string]any, error)
	DonorMessages(context.Context, string, pgtype.UUID, int64) (map[string]any, error)
	DonorActions(context.Context, string, pgtype.UUID) ([]queries.StaffActionPageRow, error)
	Enrolments(context.Context, string) (map[string]any, error)
	RequestClaim(context.Context, string, pgtype.UUID) error
	ResendMessage(context.Context, string, pgtype.UUID, pgtype.UUID) (map[string]any, error)
}

// adminCall runs a staff request; the store authorizes the session and permission.
func (a *api) adminCall(w http.ResponseWriter, r *http.Request, read func(context.Context, string) (any, error)) {
	if a.Admin == nil {
		writeError(w, r, http.StatusServiceUnavailable, staffUnavailable, nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := read(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, staffStatus)
}

func (a *api) searchDonors(w http.ResponseWriter, r *http.Request) {
	offset, ok := offsetParam(r, "offset")
	if !ok {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.SearchDonors(ctx, session, r.URL.Query().Get("q"), offset)
	})
}

func (a *api) donorDetail(w http.ResponseWriter, r *http.Request) {
	id, okID := pathUUID(r, "donor_id")
	gifts, okGifts := offsetParam(r, "gifts_offset")
	referrals, okReferrals := offsetParam(r, "referrals_offset")
	if !okID || !okGifts || !okReferrals {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.DonorDetail(ctx, session, id, gifts, referrals)
	})
}

func (a *api) donorMessages(w http.ResponseWriter, r *http.Request) {
	id, okID := pathUUID(r, "donor_id")
	offset, okOffset := offsetParam(r, "offset")
	if !okID || !okOffset {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.DonorMessages(ctx, session, id, offset)
	})
}

func (a *api) donorActions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "donor_id")
	if !ok {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.DonorActions(ctx, session, id)
	})
}

func (a *api) auditEvents(w http.ResponseWriter, r *http.Request) {
	offset, ok := offsetParam(r, "offset")
	if !ok {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.AuditEvents(ctx, session, offset)
	})
}

func (a *api) enrolments(w http.ResponseWriter, r *http.Request) {
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.Enrolments(ctx, session)
	})
}

func (a *api) requestClaim(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "donor_id")
	if !ok {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return map[string]bool{"queued": true}, a.Admin.RequestClaim(ctx, session, id)
	})
}

func (a *api) resendMessage(w http.ResponseWriter, r *http.Request) {
	id, okID := pathUUID(r, "message_id")
	key, okKey := idempotencyKey(r)
	if !okID || !okKey {
		staffInvalid(w, r)
		return
	}
	a.adminCall(w, r, func(ctx context.Context, session string) (any, error) {
		return a.Admin.ResendMessage(ctx, session, id, key)
	})
}
