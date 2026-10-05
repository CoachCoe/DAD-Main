package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/store"
)

type ContentAdminSource interface {
	Authorize(context.Context, string) error
	Inventory(context.Context, string) (map[string]any, error)
	StoreSource(context.Context, string, string, []byte) (map[string]any, error)
	ReadSource(context.Context, string, pgtype.UUID) (store.ContentDocument, error)
	Publish(context.Context, string, store.PublicationRequest) (map[string]any, error)
	Partners(context.Context, string, int64) (map[string]any, error)
}

type PartnerSource interface {
	Signup(context.Context, pgtype.UUID, store.PartnerRequest) error
}

const (
	maxSourceBytes      = 10 << 20
	maxPublicationBytes = 8 << 20
)

func (a *api) contentAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.ContentAdmin == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Content administration is temporarily unavailable", nil)
		return false
	}
	return true
}

func (a *api) contentInventory(w http.ResponseWriter, r *http.Request) {
	if !a.contentAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := a.ContentAdmin.Inventory(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, staffStatus)
}

func (a *api) partnerInquiries(w http.ResponseWriter, r *http.Request) {
	if !a.contentAvailable(w, r) {
		return
	}
	offset, ok := offsetParam(r, "offset")
	if !ok {
		staffInvalid(w, r)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := a.ContentAdmin.Partners(ctx, cookieValue(r, staffCookie), offset)
	respond(w, r, body, err, staffStatus)
}

// Uploads are authorized before the body is read so anonymous clients cannot
// make the API buffer large documents.
func (a *api) uploadSource(w http.ResponseWriter, r *http.Request) {
	if !a.contentAvailable(w, r) {
		return
	}
	session := cookieValue(r, staffCookie)
	ctx, cancel := withTimeout(r, staffTimeout)
	defer cancel()
	if err := a.ContentAdmin.Authorize(ctx, session); err != nil {
		respond(w, r, nil, err, staffStatus)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSourceBytes))
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeError(w, r, http.StatusRequestEntityTooLarge, "Source document exceeds 10 MB", nil)
		return
	}
	if err != nil {
		staffInvalid(w, r)
		return
	}
	body, err := a.ContentAdmin.StoreSource(ctx, session, r.URL.Query().Get("name"), data)
	respond(w, r, body, err, staffStatus)
}

func (a *api) downloadSource(w http.ResponseWriter, r *http.Request) {
	if !a.contentAvailable(w, r) {
		return
	}
	id, ok := pathUUID(r, "source_id")
	if !ok {
		staffInvalid(w, r)
		return
	}
	ctx, cancel := withTimeout(r, staffTimeout)
	defer cancel()
	document, err := a.ContentAdmin.ReadSource(ctx, cookieValue(r, staffCookie), id)
	if err != nil {
		respond(w, r, nil, err, staffStatus)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": document.Name}))
	_, _ = w.Write(document.Data)
}

func (a *api) publish(w http.ResponseWriter, r *http.Request) {
	if !a.contentAvailable(w, r) {
		return
	}
	session := cookieValue(r, staffCookie)
	ctx, cancel := withTimeout(r, staffTimeout)
	defer cancel()
	if err := a.ContentAdmin.Authorize(ctx, session); err != nil {
		respond(w, r, nil, err, staffStatus)
		return
	}
	var body store.PublicationRequest
	if !decodeJSONLimit(w, r, &body, maxPublicationBytes) {
		staffInvalid(w, r)
		return
	}
	result, err := a.ContentAdmin.Publish(ctx, session, body)
	respond(w, r, result, err, staffStatus)
}

func partnerStatus(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrStaffInput):
		return http.StatusUnprocessableEntity, "Check the highlighted fields and try again"
	case errors.Is(err, store.ErrStaffConflict):
		return http.StatusConflict, "This request was already used for a different submission. Start a new submission"
	default:
		return http.StatusServiceUnavailable, "Partner signup is temporarily unavailable"
	}
}

func (a *api) partnerSignup(w http.ResponseWriter, r *http.Request) {
	if a.Partner == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Partner signup is temporarily unavailable", nil)
		return
	}
	key, ok := idempotencyKey(r)
	var body store.PartnerRequest
	if !ok || !decodeJSON(w, r, &body) {
		invalidFields(w, r)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	err := a.Partner.Signup(ctx, key, body)
	respond(w, r, map[string]any{"received": true, "mode": "test"}, err, partnerStatus)
}
