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
	Details(context.Context, string) (map[string]any, error)
	AmountOptions(context.Context, string) (map[string]any, error)
	PauseOptions(context.Context, string) (map[string]any, error)
	ResumeOptions(context.Context, string) (map[string]any, error)
	PaymentMethodStatus(context.Context, string) (map[string]any, error)
	Cancel(context.Context, string, store.CancellationRequest) (map[string]any, error)
	Pause(context.Context, string, store.PauseRequest) (map[string]any, error)
	Resume(context.Context, string, store.ResumptionRequest) (map[string]any, error)
	ChangeAmount(context.Context, string, store.AmountRequest) (map[string]any, error)
	BeginPaymentUpdate(context.Context, string, pgtype.UUID) (map[string]any, error)
}

func recurringStatus(err error) (int, string) {
	if errors.Is(err, store.ErrSubscriptionConflict) || errors.Is(err, payments.ErrFacts) {
		return http.StatusConflict, "Billing details changed or require reconciliation. Refresh before confirming"
	}
	return donorStatus(err)
}

func (a *api) recurringAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.Recurring == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Recurring giving is temporarily unavailable", nil)
		return false
	}
	return true
}

// Recurring reads reconcile with the processor, so they get the provider budget.
func (a *api) recurringRead(read func(RecurringSource, context.Context, string) (map[string]any, error)) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, session string) {
		if !a.recurringAvailable(w, r) {
			return
		}
		ctx, cancel := withTimeout(r, providerTimeout)
		defer cancel()
		body, err := read(a.Recurring, ctx, session)
		respond(w, r, body, err, recurringStatus)
	}
}

type requestBody interface{ valid() bool }

// recurringCommand runs an idempotent donor command against their subscription.
func recurringCommand[T requestBody](a *api, run func(RecurringSource, context.Context, string, pgtype.UUID, T) (map[string]any, error)) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, session string) {
		if !a.recurringAvailable(w, r) {
			return
		}
		var body T
		key, ok := idempotencyKey(r)
		if !ok || !decodeJSON(w, r, &body) || !body.valid() {
			invalidFields(w, r)
			return
		}
		ctx, cancel := withTimeout(r, providerTimeout)
		defer cancel()
		result, err := run(a.Recurring, ctx, session, key, body)
		respond(w, r, result, err, recurringStatus)
	}
}

func validAmount(minor int64) bool { return minor >= minAmountMinor && minor <= maxAmountMinor }

type cancelBody struct {
	ID  pgtype.UUID `json:"subscription_id"`
	End time.Time   `json:"period_end"`
}

func (b cancelBody) valid() bool { return b.ID.Valid && !b.End.IsZero() }

func cancelRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b cancelBody) (map[string]any, error) {
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

func pauseRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b pauseBody) (map[string]any, error) {
	return s.Pause(ctx, session, store.PauseRequest{RequestID: key, AttemptID: b.ID, Months: b.Months, StartsAt: b.Start.Truncate(time.Microsecond), ResumesAt: b.End.Truncate(time.Microsecond)})
}

type resumeBody struct {
	ID   pgtype.UUID `json:"pause_id"`
	Next time.Time   `json:"next_renewal"`
}

func (b resumeBody) valid() bool { return b.ID.Valid && !b.Next.IsZero() }

func resumeRecurring(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b resumeBody) (map[string]any, error) {
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

func changeAmount(s RecurringSource, ctx context.Context, session string, key pgtype.UUID, b amountBody) (map[string]any, error) {
	return s.ChangeAmount(ctx, session, store.AmountRequest{RequestID: key, AttemptID: b.ID, Amount: b.Amount, Previous: b.Previous, EffectiveAt: b.Effective.Truncate(time.Microsecond)})
}

func (a *api) beginPaymentUpdate(w http.ResponseWriter, r *http.Request, session string) {
	if !a.recurringAvailable(w, r) {
		return
	}
	key, ok := idempotencyKey(r)
	if !ok {
		invalidFields(w, r)
		return
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	body, err := a.Recurring.BeginPaymentUpdate(ctx, session, key)
	respond(w, r, body, err, recurringStatus)
}
