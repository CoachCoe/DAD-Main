package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type StaffFinanceSource interface {
	RefundQuote(context.Context, string, pgtype.UUID, pgtype.UUID) (map[string]interface{}, error)
	CancellationQuote(context.Context, string, pgtype.UUID) (map[string]interface{}, error)
	Refund(context.Context, string, store.StaffFinancialRequest) (map[string]interface{}, error)
	Cancel(context.Context, string, store.StaffFinancialRequest) (map[string]interface{}, error)
}

func installStaffFinanceRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.StaffFinance
	respond := func(w http.ResponseWriter, body interface{}, err error) {
		if errors.Is(err, payments.ErrFacts) || errors.Is(err, store.ErrSubscriptionConflict) {
			reply(w, 409, map[string]string{"detail": "Financial records require reconciliation. Refresh before confirming"})
			return
		}
		staffReply(w, reply, body, err)
	}
	for _, action := range []string{"refund", "cancel"} {
		path := "/donor/staff/donors/{donor_id}/cancellation"
		if action == "refund" {
			path = "/donor/staff/donors/{donor_id}/gifts/{gift_id}/refund"
		}
		for _, method := range []string{"GET", "POST"} {
			mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
				if method == "POST" && !guard.check(w, r, reply) {
					return
				}
				var donorID, giftID pgtype.UUID
				if donorID.Scan(r.PathValue("donor_id")) != nil || !donorID.Valid {
					staffReply(w, reply, nil, store.ErrStaffInput)
					return
				}
				if action == "refund" && (giftID.Scan(r.PathValue("gift_id")) != nil || !giftID.Valid) {
					staffReply(w, reply, nil, store.ErrStaffInput)
					return
				}
				if source == nil {
					reply(w, 503, map[string]string{"detail": "Financial actions are temporarily unavailable"})
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
				defer cancel()
				if method == "GET" {
					var body map[string]interface{}
					var err error
					if action == "refund" {
						body, err = source.RefundQuote(ctx, staffToken(r), donorID, giftID)
					} else {
						body, err = source.CancellationQuote(ctx, staffToken(r), donorID)
					}
					respond(w, body, err)
					return
				}
				var key pgtype.UUID
				if key.Scan(r.Header.Get("Idempotency-Key")) != nil || !key.Valid || key.Bytes[6]>>4 != 4 || key.Bytes[8]&0xc0 != 0x80 {
					staffReply(w, reply, nil, store.ErrStaffInput)
					return
				}
				request := store.StaffFinancialRequest{RequestID: key, DonorID: donorID, TargetID: giftID}
				if action == "refund" {
					var body struct {
						Amount int64  `json:"amount_minor"`
						Reason string `json:"reason"`
					}
					if !decodeBody(w, r, &body) || body.Amount < 1 || body.Amount > 99999999 {
						staffReply(w, reply, nil, store.ErrStaffInput)
						return
					}
					request.Amount, request.Reason = body.Amount, body.Reason
				} else {
					var body struct {
						ID     pgtype.UUID `json:"subscription_id"`
						End    time.Time   `json:"period_end"`
						Reason string      `json:"reason"`
					}
					if !decodeBody(w, r, &body) || !body.ID.Valid || body.End.IsZero() {
						staffReply(w, reply, nil, store.ErrStaffInput)
						return
					}
					request.TargetID, request.PeriodEnd, request.Reason = body.ID, body.End.Truncate(time.Microsecond), body.Reason
				}
				if utf8.RuneCountInString(request.Reason) < 5 || utf8.RuneCountInString(request.Reason) > 500 {
					staffReply(w, reply, nil, store.ErrStaffInput)
					return
				}
				var body map[string]interface{}
				var err error
				if action == "refund" {
					body, err = source.Refund(ctx, staffToken(r), request)
				} else {
					body, err = source.Cancel(ctx, staffToken(r), request)
				}
				respond(w, body, err)
			})
		}
	}
}
