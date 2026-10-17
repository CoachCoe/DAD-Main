package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store"
)

type RecurringSource interface {
	Details(context.Context, string) (store.RecurringDetails, error)
	AmountOptions(context.Context, string) (store.AmountOptions, error)
	PauseOptions(context.Context, string) (store.PauseOptions, error)
	ResumeOptions(context.Context, string) (store.ResumeOptions, error)
	PaymentMethodStatus(context.Context, string) (store.PaymentMethodStatus, error)
	Cancel(context.Context, string, store.CancellationRequest) (store.RecurringDetails, error)
	Pause(context.Context, string, store.PauseRequest) (store.PauseResult, error)
	Resume(context.Context, string, store.ResumptionRequest) (store.Resumption, error)
	ChangeAmount(context.Context, string, store.AmountRequest) (store.AmountChange, error)
	BeginPaymentUpdate(context.Context, string, pgtype.UUID) (store.PaymentUpdate, error)
}

func recurringStatus(err error) (int, string) {
	if errors.Is(err, store.ErrSubscriptionConflict) || errors.Is(err, payments.ErrFacts) {
		return http.StatusConflict, "Billing details changed or require reconciliation. Refresh before confirming"
	}
	return donorStatus(err)
}

// Recurring views read stored state; commands call the processor, so they get
// its time budget.
func (a *api) recurring(timeout time.Duration) service {
	return service{a.Recurring != nil, "Recurring giving is temporarily unavailable", timeout, recurringStatus}
}

func recurringRead[R any](a *api, read func(RecurringSource, context.Context, string) (R, error)) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, session string) {
		a.recurring(readTimeout).serve(w, r, func(ctx context.Context) (any, error) { return read(a.Recurring, ctx, session) })
	}
}

// recurringCommand runs an idempotent donor command against their subscription.
func recurringCommand[T requestBody, R any](a *api, run func(RecurringSource, context.Context, string, pgtype.UUID, T) (R, error)) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, session string) {
		key, ok := idempotencyKey(w, r)
		if !ok {
			return
		}
		body, ok := decodeValid[T](w, r)
		if !ok {
			return
		}
		a.recurring(providerTimeout).serve(w, r, func(ctx context.Context) (any, error) { return run(a.Recurring, ctx, session, key, body) })
	}
}

type cancelBody struct {
	ID  pgtype.UUID `json:"subscription_id"`
	End time.Time   `json:"period_end"`
}

func (b cancelBody) valid() bool { return b.ID.Valid && !b.End.IsZero() }

func cancelRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b cancelBody) (store.RecurringDetails, error) {
	return s.Cancel(ctx, session, store.CancellationRequest{RequestID: key, AttemptID: b.ID, PeriodEnd: b.End.Truncate(time.Microsecond)})
}

type pauseBody struct {
	ID     pgtype.UUID `json:"subscription_id"`
	Months int32       `json:"months"`
	Start  time.Time   `json:"starts_at"`
	End    time.Time   `json:"resumes_at"`
}

func (b pauseBody) valid() bool {
	return b.ID.Valid && b.Months >= 1 && b.Months <= 3 && !b.Start.IsZero() && !b.End.IsZero()
}

func pauseRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b pauseBody) (store.PauseResult, error) {
	return s.Pause(ctx, session, store.PauseRequest{RequestID: key, AttemptID: b.ID, Months: b.Months, StartsAt: b.Start.Truncate(time.Microsecond), ResumesAt: b.End.Truncate(time.Microsecond)})
}

type resumeBody struct {
	ID   pgtype.UUID `json:"pause_id"`
	Next time.Time   `json:"next_renewal"`
}

func (b resumeBody) valid() bool { return b.ID.Valid && !b.Next.IsZero() }

func resumeRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b resumeBody) (store.Resumption, error) {
	return s.Resume(ctx, session, store.ResumptionRequest{RequestID: key, PauseID: b.ID, NextRenewal: b.Next.Truncate(time.Microsecond)})
}

type amountBody struct {
	ID        pgtype.UUID `json:"subscription_id"`
	Amount    int64       `json:"amount_minor"`
	Previous  int64       `json:"previous_minor"`
	Effective time.Time   `json:"effective_at"`
}

func (b amountBody) valid() bool {
	return b.ID.Valid && validAmount(b.Amount) && validAmount(b.Previous) && !b.Effective.IsZero()
}

func changeAmount(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b amountBody) (store.AmountChange, error) {
	return s.ChangeAmount(ctx, session, store.AmountRequest{RequestID: key, AttemptID: b.ID, Amount: b.Amount, Previous: b.Previous, EffectiveAt: b.Effective.Truncate(time.Microsecond)})
}

func (a *api) beginPaymentUpdate(w http.ResponseWriter, r *http.Request, session string) {
	if key, ok := idempotencyKey(w, r); ok {
		a.recurring(providerTimeout).serve(w, r, func(ctx context.Context) (any, error) { return a.Recurring.BeginPaymentUpdate(ctx, session, key) })
	}
}
