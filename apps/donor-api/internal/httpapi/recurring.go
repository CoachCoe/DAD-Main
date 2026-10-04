package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type RecurringSource interface {
	Details(context.Context, string) (map[string]interface{}, error)
	AmountOptions(context.Context, string) (map[string]interface{}, error)
	PauseOptions(context.Context, string) (map[string]interface{}, error)
	ResumeOptions(context.Context, string) (map[string]interface{}, error)
	PaymentMethodStatus(context.Context, string) (map[string]interface{}, error)
	Cancel(context.Context, string, store.CancellationRequest) (map[string]interface{}, error)
	Pause(context.Context, string, store.PauseRequest) (map[string]interface{}, error)
	Resume(context.Context, string, store.ResumptionRequest) (map[string]interface{}, error)
	ChangeAmount(context.Context, string, store.AmountRequest) (map[string]interface{}, error)
	BeginPaymentUpdate(context.Context, string, pgtype.UUID) (map[string]interface{}, error)
}

func recurringReply(w http.ResponseWriter, reply replyFunc, body interface{}, err error) {
	switch {
	case errors.Is(err, store.ErrSubscriptionConflict), errors.Is(err, payments.ErrFacts):
		reply(w, 409, map[string]string{"detail": "Billing details changed or require reconciliation. Refresh before confirming"})
	default:
		donorReply(w, reply, body, err)
	}
}

func recurringPost[T any](mux *http.ServeMux, route string, guard *browserGuard, reply replyFunc, ready func(http.ResponseWriter) bool, valid func(T) bool, call func(context.Context, string, pgtype.UUID, T) (map[string]interface{}, error)) {
	mux.HandleFunc("POST /donor/me/"+route, func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var key pgtype.UUID
		var body T
		if key.Scan(r.Header.Get("Idempotency-Key")) != nil || !key.Valid || key.Bytes[6]>>4 != 4 || key.Bytes[8]&0xc0 != 0x80 || !decodeBody(w, r, &body) || !valid(body) {
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := call(ctx, sessionToken(r), key, body)
		recurringReply(w, reply, result, err)
	})
}

func installRecurringRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.Recurring
	ready := func(w http.ResponseWriter) bool {
		if source != nil {
			return true
		}
		reply(w, 503, map[string]string{"detail": "Recurring giving is temporarily unavailable"})
		return false
	}
	// Bind methods only when configured; unconfigured routes still return 503.
	reads := map[string]func(context.Context, string) (map[string]interface{}, error){"recurring": nil, "recurring/amount": nil, "recurring/pause": nil, "recurring/resume": nil, "payment-method": nil}
	if source != nil {
		reads["recurring"] = source.Details
		reads["recurring/amount"] = source.AmountOptions
		reads["recurring/pause"] = source.PauseOptions
		reads["recurring/resume"] = source.ResumeOptions
		reads["payment-method"] = source.PaymentMethodStatus
	}
	for path, call := range reads {
		mux.HandleFunc("GET /donor/me/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			body, err := call(ctx, sessionToken(r))
			recurringReply(w, reply, body, err)
		})
	}
	type cancellation struct {
		ID  pgtype.UUID `json:"subscription_id"`
		End time.Time   `json:"period_end"`
	}
	recurringPost(mux, "recurring/cancel", guard, reply, ready, func(b cancellation) bool { return b.ID.Valid && !b.End.IsZero() }, func(ctx context.Context, t string, k pgtype.UUID, b cancellation) (map[string]interface{}, error) {
		return source.Cancel(ctx, t, store.CancellationRequest{RequestID: k, AttemptID: b.ID, PeriodEnd: b.End.Truncate(time.Microsecond)})
	})
	type pause struct {
		ID     pgtype.UUID `json:"subscription_id"`
		Months int32       `json:"months"`
		Start  time.Time   `json:"starts_at"`
		End    time.Time   `json:"resumes_at"`
	}
	recurringPost(mux, "recurring/pause", guard, reply, ready, func(b pause) bool {
		return b.ID.Valid && b.Months >= 1 && b.Months <= 3 && !b.Start.IsZero() && !b.End.IsZero()
	}, func(ctx context.Context, t string, k pgtype.UUID, b pause) (map[string]interface{}, error) {
		return source.Pause(ctx, t, store.PauseRequest{RequestID: k, AttemptID: b.ID, Months: b.Months, StartsAt: b.Start.Truncate(time.Microsecond), ResumesAt: b.End.Truncate(time.Microsecond)})
	})
	type resume struct {
		ID   pgtype.UUID `json:"pause_id"`
		Next time.Time   `json:"next_renewal"`
	}
	recurringPost(mux, "recurring/resume", guard, reply, ready, func(b resume) bool { return b.ID.Valid && !b.Next.IsZero() }, func(ctx context.Context, t string, k pgtype.UUID, b resume) (map[string]interface{}, error) {
		return source.Resume(ctx, t, store.ResumptionRequest{RequestID: k, PauseID: b.ID, NextRenewal: b.Next.Truncate(time.Microsecond)})
	})
	type amount struct {
		ID        pgtype.UUID `json:"subscription_id"`
		Amount    int64       `json:"amount_minor"`
		Previous  int64       `json:"previous_minor"`
		Effective time.Time   `json:"effective_at"`
	}
	recurringPost(mux, "recurring/amount", guard, reply, ready, func(b amount) bool {
		return b.ID.Valid && b.Amount >= 100 && b.Amount <= 99999999 && b.Previous >= 100 && b.Previous <= 99999999 && !b.Effective.IsZero()
	}, func(ctx context.Context, t string, k pgtype.UUID, b amount) (map[string]interface{}, error) {
		return source.ChangeAmount(ctx, t, store.AmountRequest{RequestID: k, AttemptID: b.ID, Amount: b.Amount, Previous: b.Previous, EffectiveAt: b.Effective.Truncate(time.Microsecond)})
	})
	mux.HandleFunc("POST /donor/me/payment-method/setup", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var key pgtype.UUID
		if key.Scan(r.Header.Get("Idempotency-Key")) != nil || !key.Valid || key.Bytes[6]>>4 != 4 || key.Bytes[8]&0xc0 != 0x80 {
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		body, err := source.BeginPaymentUpdate(ctx, sessionToken(r), key)
		recurringReply(w, reply, body, err)
	})
}
