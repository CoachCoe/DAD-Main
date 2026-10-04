package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store"
)

type ContentAdminSource interface {
	Authorize(context.Context, string) error
	Inventory(context.Context, string) (map[string]interface{}, error)
	StoreSource(context.Context, string, string, []byte) (map[string]interface{}, error)
	ReadSource(context.Context, string, pgtype.UUID) (store.ContentDocument, error)
	Publish(context.Context, string, store.PublicationRequest) (map[string]interface{}, error)
	Partners(context.Context, string, int64) (map[string]interface{}, error)
}
type PartnerSource interface {
	Signup(context.Context, pgtype.UUID, store.PartnerRequest) error
}

func installContentRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.ContentAdmin
	ready := func(w http.ResponseWriter) bool {
		if source != nil {
			return true
		}
		reply(w, 503, map[string]string{"detail": "Content administration is temporarily unavailable"})
		return false
	}
	for _, path := range []string{"content", "partners"} {
		mux.HandleFunc("GET /donor/staff/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w) {
				return
			}
			offset, err := offsetQuery(r, "offset")
			if err != nil {
				staffReply(w, reply, nil, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var body map[string]interface{}
			if path == "content" {
				body, err = source.Inventory(ctx, staffToken(r))
			} else {
				body, err = source.Partners(ctx, staffToken(r), offset)
			}
			staffReply(w, reply, body, err)
		})
	}
	mux.HandleFunc("POST /donor/staff/content/sources", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := source.Authorize(ctx, staffToken(r)); err != nil {
			staffReply(w, reply, nil, err)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				reply(w, 413, map[string]string{"detail": "Source document exceeds 10 MB"})
			} else {
				staffReply(w, reply, nil, store.ErrStaffInput)
			}
			return
		}
		body, err := source.StoreSource(ctx, staffToken(r), r.URL.Query().Get("name"), data)
		staffReply(w, reply, body, err)
	})
	mux.HandleFunc("GET /donor/staff/content/sources/{source_id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var id pgtype.UUID
		if id.Scan(r.PathValue("source_id")) != nil || !id.Valid {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		document, err := source.ReadSource(ctx, staffToken(r), id)
		if err != nil {
			staffReply(w, reply, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": document.Name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(document.Data)
	})
	mux.HandleFunc("POST /donor/staff/content/publish", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := source.Authorize(ctx, staffToken(r)); err != nil {
			staffReply(w, reply, nil, err)
			return
		}
		var body store.PublicationRequest
		if !decodeBodyLimit(w, r, &body, 8*1024*1024) {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		result, err := source.Publish(ctx, staffToken(r), body)
		staffReply(w, reply, result, err)
	})
	mux.HandleFunc("POST /donor/public/partner-signup", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var id pgtype.UUID
		var body store.PartnerRequest
		if id.Scan(r.Header.Get("Idempotency-Key")) != nil || !id.Valid || !decodeBody(w, r, &body) {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		if services.Partner == nil {
			reply(w, 503, map[string]string{"detail": "Partner signup is temporarily unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := services.Partner.Signup(ctx, id, body)
		staffReply(w, reply, map[string]interface{}{"received": true, "mode": "test"}, err)
	})
}
