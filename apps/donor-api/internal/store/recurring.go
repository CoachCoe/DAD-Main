package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

var ErrSubscriptionConflict = errors.New("billing details changed or require reconciliation; refresh before confirming")

type RecurringService struct {
	DB      *pgxpool.Pool
	Gateway payments.SubscriptionGateway
	Origin  string
}
type CancellationRequest struct {
	RequestID, AttemptID pgtype.UUID
	PeriodEnd            time.Time
}
type PauseRequest struct {
	RequestID, AttemptID pgtype.UUID
	Months               int32
	StartsAt, ResumesAt  time.Time
}
type ResumptionRequest struct {
	RequestID, PauseID pgtype.UUID
	NextRenewal        time.Time
}
type AmountRequest struct {
	RequestID, AttemptID pgtype.UUID
	Amount, Previous     int64
	EffectiveAt          time.Time
}
type ownerResolver func(context.Context, pgx.Tx, time.Time) (queries.Donor, error)

func donorOwner(token string) ownerResolver {
	return func(ctx context.Context, tx pgx.Tx, now time.Time) (queries.Donor, error) {
		q := queries.New(tx)
		d, err := Authenticated(ctx, q, token, now)
		if err != nil {
			return d, err
		}
		if _, err = q.LockDonor(ctx, d.ID); err != nil {
			return d, err
		}
		return Authenticated(ctx, q, token, now)
	}
}
func (s RecurringService) transaction(ctx context.Context, owner ownerResolver, operation func(pgx.Tx, queries.Donor, time.Time) (map[string]interface{}, error)) (map[string]interface{}, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	d, err := owner(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	result, err := operation(tx, d, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func requestIDValid(id pgtype.UUID) bool {
	return id.Valid && id.Bytes[6]>>4 == 4 && id.Bytes[8]&0xc0 == 0x80
}
func ownedAttempt(ctx context.Context, q *queries.Queries, donor, id pgtype.UUID) (queries.DonorStripeCheckout, error) {
	row, err := q.OwnedMonthlyAttempt(ctx, queries.OwnedMonthlyAttemptParams{DonorID: donor, ID: id})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !row.StripeSubscriptionID.Valid) {
		return row, ErrSubscriptionConflict
	}
	return row, err
}
func nextAmount(ctx context.Context, q *queries.Queries, draft queries.DonorCheckout) (int64, error) {
	amount, err := q.LatestConfirmedAmount(ctx, draft.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft.AmountMinor, nil
	}
	return amount, err
}
func subscriptionView(ctx context.Context, q *queries.Queries, attempt queries.DonorStripeCheckout, row queries.DonorSubscription) (map[string]interface{}, error) {
	draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
	if err != nil {
		return nil, err
	}
	amount, err := nextAmount(ctx, q, draft)
	if err != nil {
		return nil, err
	}
	var stops interface{}
	if row.StopsAt.Valid {
		stops = row.StopsAt.Time
	}
	return map[string]interface{}{"mode": "test", "state": "ready", "subscription": map[string]interface{}{"id": attempt.ID.String(), "amount_minor": amount, "currency": "USD", "status": row.Status, "period_end": row.PeriodEnd.Time, "stops_at": stops}}, nil
}
func (s RecurringService) read(ctx context.Context, tx pgx.Tx, attempt queries.DonorStripeCheckout, now time.Time, policy string) (queries.DonorSubscription, payments.Object, error) {
	empty := queries.DonorSubscription{}
	if s.Gateway == nil {
		return empty, nil, payments.ErrCheckoutUnavailable
	}
	remote, err := s.Gateway.RetrieveSubscription(ctx, attempt.StripeSubscriptionID.String)
	if err != nil {
		return empty, nil, err
	}
	draft, err := queries.New(tx).CheckoutDraft(ctx, attempt.CheckoutID)
	if err != nil {
		return empty, nil, err
	}
	row, err := reconcileRemoteSubscription(ctx, tx, draft, attempt, remote, now)
	if err != nil {
		return empty, nil, err
	}
	if policy != "" && (!absent(remote, "schedule") || !absent(remote, "pending_update")) {
		return empty, nil, ErrSubscriptionConflict
	}
	if policy == "amount" && !absent(remote, "pause_collection") {
		return empty, nil, ErrSubscriptionConflict
	}
	if policy == "pause" {
		anchor, err := remote.Int("billing_cycle_anchor")
		if err != nil || anchor < 0 || anchor > 253402300799 {
			return empty, nil, payments.ErrFacts
		}
	}
	return row, remote, nil
}
func (s RecurringService) Details(ctx context.Context, token string) (map[string]interface{}, error) {
	return s.transaction(ctx, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]interface{}{"mode": "test", "state": "ready", "subscription": nil}, nil
		}
		if err != nil {
			return nil, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return map[string]interface{}{"mode": "test", "state": "preparing", "subscription": nil}, nil
		}
		row, _, err := s.read(ctx, tx, attempt, now, "")
		if err != nil {
			return nil, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return nil, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
func (s RecurringService) Cancel(ctx context.Context, token string, request CancellationRequest) (map[string]interface{}, error) {
	return s.cancelAuthorized(ctx, donorOwner(token), request)
}
func (s RecurringService) cancelAuthorized(ctx context.Context, owner ownerResolver, r CancellationRequest) (map[string]interface{}, error) {
	if !requestIDValid(r.RequestID) || r.PeriodEnd.IsZero() {
		return nil, payments.ErrFacts
	}
	_, err := s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		action, err := q.CancellationByID(ctx, r.RequestID)
		if err == nil {
			if action.DonorID != d.ID || action.AttemptID != attempt.ID || !action.ConfirmedPeriodEnd.Time.Equal(r.PeriodEnd) {
				return nil, ErrSubscriptionConflict
			}
			return nil, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, q.InsertCancellation(ctx, queries.InsertCancellationParams{ID: r.RequestID, DonorID: d.ID, AttemptID: attempt.ID, ConfirmedPeriodEnd: timestamp(r.PeriodEnd), CreatedAt: timestamp(now)})
	})
	if err != nil {
		return nil, err
	}
	return s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		action, err := q.CancellationByID(ctx, r.RequestID)
		if err != nil {
			return nil, err
		}
		row, _, err := s.read(ctx, tx, attempt, now, "")
		if err != nil {
			return nil, err
		}
		if !action.CompletedAt.Valid {
			if !row.StopsAt.Valid {
				if !row.PeriodEnd.Time.Equal(r.PeriodEnd) || !now.Before(r.PeriodEnd) || now.Sub(action.CreatedAt.Time) >= 23*time.Hour {
					return nil, ErrSubscriptionConflict
				}
				switch row.Status {
				case "active", "past_due", "unpaid":
				default:
					return nil, ErrSubscriptionConflict
				}
				if err = s.Gateway.ScheduleCancellation(ctx, attempt.StripeSubscriptionID.String, r.PeriodEnd.Unix(), "dad-cancel-"+action.ID.String()); err != nil {
					return nil, err
				}
				row, _, err = s.read(ctx, tx, attempt, now, "")
				if err != nil {
					return nil, err
				}
				if !row.StopsAt.Valid {
					return nil, ErrSubscriptionConflict
				}
			}
			if err = q.CompleteCancellation(ctx, queries.CompleteCancellationParams{ID: action.ID, CompletedAt: timestamp(now)}); err != nil {
				return nil, err
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return nil, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
func effectiveResume(ctx context.Context, q *queries.Queries, pause queries.DonorPause) (time.Time, error) {
	resume, err := q.ResumptionForPause(ctx, pause.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, err
	}
	if err == nil && resume.ConfirmedAt.Valid {
		return resume.NextRenewal.Time, nil
	}
	return pause.ResumesAt.Time, nil
}
func renewalAfter(boundary time.Time, months int32, anchor time.Time) (time.Time, error) {
	boundary, anchor = boundary.UTC(), anchor.UTC()
	if months < 1 || months > 3 {
		return time.Time{}, payments.ErrFacts
	}
	day := min(anchor.Day(), time.Date(boundary.Year(), boundary.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	expected := time.Date(boundary.Year(), boundary.Month(), day, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, time.UTC)
	if !boundary.Equal(expected) {
		return time.Time{}, payments.ErrFacts
	}
	next := time.Date(boundary.Year(), boundary.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	day = min(anchor.Day(), time.Date(next.Year(), next.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	return time.Date(next.Year(), next.Month(), day, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, time.UTC), nil
}
func pauseView(ctx context.Context, q *queries.Queries, p queries.DonorPause) (map[string]interface{}, error) {
	until, err := effectiveResume(ctx, q, p)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"request_id": p.ID.String(), "months": p.Months, "starts_at": p.StartsAt.Time, "resumes_at": until, "confirmed": p.ConfirmedAt.Valid}, nil
}
func amountView(c queries.DonorAmountChange) map[string]interface{} {
	return map[string]interface{}{"request_id": c.ID.String(), "amount_minor": c.AmountMinor, "previous_minor": c.PreviousMinor, "effective_at": c.EffectiveAt.Time, "confirmed": c.ConfirmedAt.Valid}
}
func resumptionView(c queries.DonorResumption) map[string]interface{} {
	return map[string]interface{}{"request_id": c.ID.String(), "pause_id": c.PauseID.String(), "next_renewal": c.NextRenewal.Time, "confirmed": c.ConfirmedAt.Valid}
}
