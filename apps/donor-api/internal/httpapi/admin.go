package httpapi

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/store/queries"
)

type AdminSource interface {
	SearchDonors(context.Context, string, string, int64) (map[string]interface{}, error)
	DonorDetail(context.Context, string, pgtype.UUID, int64, int64) (map[string]interface{}, error)
	AuditEvents(context.Context, string, int64) (map[string]interface{}, error)
	DonorMessages(context.Context, string, pgtype.UUID, int64) (map[string]interface{}, error)
	DonorActions(context.Context, string, pgtype.UUID) ([]queries.StaffActionPageRow, error)
	Enrolments(context.Context, string) (map[string]interface{}, error)
	RequestClaim(context.Context, string, pgtype.UUID) error
	ResendMessage(context.Context, string, pgtype.UUID, pgtype.UUID) (map[string]interface{}, error)
}

func offsetQuery(r *http.Request, key string) (int64, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64-50 {
		return 0, store.ErrStaffInput
	}
	return n, nil
}

func installAdminRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.Admin
	ready := func(w http.ResponseWriter) bool {
		if source != nil {
			return true
		}
		reply(w, 503, map[string]string{"detail": "Staff service is temporarily unavailable"})
		return false
	}
	mux.HandleFunc("GET /donor/staff/donors", func(w http.ResponseWriter, r *http.Request) {
		offset, err := offsetQuery(r, "offset")
		if err != nil {
			staffReply(w, reply, nil, err)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		body, err := source.SearchDonors(ctx, staffToken(r), r.URL.Query().Get("q"), offset)
		staffReply(w, reply, body, err)
	})
	for _, suffix := range []string{"", "/messages", "/actions"} {
		mux.HandleFunc("GET /donor/staff/donors/{donor_id}"+suffix, func(w http.ResponseWriter, r *http.Request) {
			var id pgtype.UUID
			if id.Scan(r.PathValue("donor_id")) != nil || !id.Valid {
				staffReply(w, reply, nil, store.ErrStaffInput)
				return
			}
			offset, err := offsetQuery(r, "offset")
			if err != nil {
				staffReply(w, reply, nil, err)
				return
			}
			gifts, err := offsetQuery(r, "gifts_offset")
			if err != nil {
				staffReply(w, reply, nil, err)
				return
			}
			referrals, err := offsetQuery(r, "referrals_offset")
			if err != nil {
				staffReply(w, reply, nil, err)
				return
			}
			if !ready(w) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var body interface{}
			switch suffix {
			case "":
				body, err = source.DonorDetail(ctx, staffToken(r), id, gifts, referrals)
			case "/messages":
				body, err = source.DonorMessages(ctx, staffToken(r), id, offset)
			case "/actions":
				body, err = source.DonorActions(ctx, staffToken(r), id)
			}
			staffReply(w, reply, body, err)
		})
	}
	for _, path := range []string{"audit", "enrolments"} {
		mux.HandleFunc("GET /donor/staff/"+path, func(w http.ResponseWriter, r *http.Request) {
			offset, err := offsetQuery(r, "offset")
			if err != nil {
				staffReply(w, reply, nil, err)
				return
			}
			if !ready(w) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var body interface{}
			if path == "audit" {
				body, err = source.AuditEvents(ctx, staffToken(r), offset)
			} else {
				body, err = source.Enrolments(ctx, staffToken(r))
			}
			staffReply(w, reply, body, err)
		})
	}
	mux.HandleFunc("POST /donor/staff/donors/{donor_id}/claim-email", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		var id pgtype.UUID
		if id.Scan(r.PathValue("donor_id")) != nil || !id.Valid {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := source.RequestClaim(ctx, staffToken(r), id)
		staffReply(w, reply, map[string]bool{"queued": true}, err)
	})
	mux.HandleFunc("POST /donor/staff/messages/{message_id}/resend", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		var id, key pgtype.UUID
		if id.Scan(r.PathValue("message_id")) != nil || !id.Valid || key.Scan(r.Header.Get("Idempotency-Key")) != nil || !key.Valid || key.Bytes[6]>>4 != 4 || key.Bytes[8]&0xc0 != 0x80 {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		body, err := source.ResendMessage(ctx, staffToken(r), id, key)
		staffReply(w, reply, body, err)
	})
}
