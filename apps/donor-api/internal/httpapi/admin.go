package httpapi

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"dad/donor-api/internal/store"
)

type AdminSource interface {
	SearchDonors(context.Context, string, string, int64) (store.DonorSearch, error)
	DonorDetail(context.Context, string, pgtype.UUID, int64, int64) (store.DonorDetail, error)
	AuditEvents(context.Context, string, int64) (store.AuditPage, error)
	DonorMessages(context.Context, string, pgtype.UUID, int64) (store.MessagePage, error)
	DonorActions(context.Context, string, pgtype.UUID, int64) (store.ActionPage, error)
	PendingAction(context.Context, string, pgtype.UUID, pgtype.UUID, string) (store.PendingAction, error)
	Enrolments(context.Context, string) (store.Enrolments, error)
	RequestClaim(context.Context, string, pgtype.UUID) error
	ResendMessage(context.Context, string, pgtype.UUID, pgtype.UUID) (store.QueuedMessage, error)
}

func (a *api) admin() service {
	return service{a.Admin != nil, staffUnavailable, readTimeout, staffStatus}
}

func (a *api) searchDonors(w http.ResponseWriter, r *http.Request, session string) {
	offset, ok := offsetParam(r, "offset")
	if !ok {
		invalidFields(w, r)
		return
	}
	term := r.URL.Query().Get("q")
	if utf8.RuneCountInString(term) > maxEmailRunes || strings.ContainsRune(term, 0) {
		writeError(w, r, http.StatusUnprocessableEntity, "Search for at most 320 characters", nil)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Admin.SearchDonors(ctx, session, term, offset)
	})
}

func (a *api) donorDetail(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "donor_id")
	if !ok {
		return
	}
	gifts, okGifts := offsetParam(r, "gifts_offset")
	referrals, okReferrals := offsetParam(r, "referrals_offset")
	if !okGifts || !okReferrals {
		invalidFields(w, r)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Admin.DonorDetail(ctx, session, id, gifts, referrals)
	})
}

func (a *api) donorMessages(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "donor_id")
	if !ok {
		return
	}
	offset, ok := offsetParam(r, "offset")
	if !ok {
		invalidFields(w, r)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Admin.DonorMessages(ctx, session, id, offset)
	})
}

func (a *api) donorActions(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "donor_id")
	if !ok {
		return
	}
	offset, ok := offsetParam(r, "offset")
	if !ok {
		invalidFields(w, r)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Admin.DonorActions(ctx, session, id, offset)
	})
}

func (a *api) pendingAction(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "donor_id")
	if !ok {
		return
	}
	var target pgtype.UUID
	kind := r.URL.Query().Get("kind")
	if target.Scan(r.URL.Query().Get("target_id")) != nil || !target.Valid || (kind != "REFUND" && kind != "CANCEL") {
		invalidFields(w, r)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Admin.PendingAction(ctx, session, id, target, kind)
	})
}

func (a *api) auditEvents(w http.ResponseWriter, r *http.Request, session string) {
	offset, ok := offsetParam(r, "offset")
	if !ok {
		invalidFields(w, r)
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) { return a.Admin.AuditEvents(ctx, session, offset) })
}

func (a *api) enrolments(w http.ResponseWriter, r *http.Request, session string) {
	a.admin().serve(w, r, func(ctx context.Context) (any, error) { return a.Admin.Enrolments(ctx, session) })
}

func (a *api) requestClaim(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "donor_id")
	if !ok {
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]bool{"queued": true}, a.Admin.RequestClaim(ctx, session, id)
	})
}

func (a *api) resendMessage(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "message_id")
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	a.admin().serve(w, r, func(ctx context.Context) (any, error) { return a.Admin.ResendMessage(ctx, session, id, key) })
}
