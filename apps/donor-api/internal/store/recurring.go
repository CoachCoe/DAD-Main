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

// The JSON views below are the web client's contract. Fields stay in
// alphabetical order so responses match the original map encoding byte for byte.

// RecurringDetails describes the donor's monthly gift. State is omitted only by
// a staff quote for a donor without one.
type RecurringDetails struct {
	Mode         string        `json:"mode"`
	State        string        `json:"state,omitempty"`
	Subscription *Subscription `json:"subscription"`
}
type Subscription struct {
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	ID          string     `json:"id"`
	PeriodEnd   time.Time  `json:"period_end"`
	Status      string     `json:"status"`
	StopsAt     *time.Time `json:"stops_at"`
}
type PauseView struct {
	Confirmed bool      `json:"confirmed"`
	Months    int32     `json:"months"`
	RequestID string    `json:"request_id"`
	ResumesAt time.Time `json:"resumes_at"`
	StartsAt  time.Time `json:"starts_at"`
}
type PauseResult struct {
	Pause PauseView `json:"pause"`
}
type AmountChange struct {
	AmountMinor   int64     `json:"amount_minor"`
	Confirmed     bool      `json:"confirmed"`
	EffectiveAt   time.Time `json:"effective_at"`
	PreviousMinor int64     `json:"previous_minor"`
	RequestID     string    `json:"request_id"`
}
type Resumption struct {
	Confirmed   bool      `json:"confirmed"`
	NextRenewal time.Time `json:"next_renewal"`
	PauseID     string    `json:"pause_id"`
	RequestID   string    `json:"request_id"`
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
		return Authenticated(ctx, q, token, time.Now().UTC())
	}
}
func ownerTransaction[T any](ctx context.Context, db *pgxpool.Pool, owner ownerResolver, operation func(pgx.Tx, queries.Donor, time.Time) (T, error)) (T, error) {
	var empty T
	tx, err := db.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	d, err := owner(ctx, tx, time.Now().UTC())
	if err != nil {
		return empty, err
	}
	result, err := operation(tx, d, time.Now().UTC())
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}

// ValidRequestID reports whether id is a version-4 UUID, the only form accepted as an idempotency key.
func ValidRequestID(id pgtype.UUID) bool {
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
func subscriptionView(ctx context.Context, q *queries.Queries, attempt queries.DonorStripeCheckout, row queries.DonorSubscription) (RecurringDetails, error) {
	draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
	if err != nil {
		return RecurringDetails{}, err
	}
	amount, err := nextAmount(ctx, q, draft)
	if err != nil {
		return RecurringDetails{}, err
	}
	sub := &Subscription{ID: attempt.ID.String(), AmountMinor: amount, Currency: "USD", Status: row.Status, PeriodEnd: row.PeriodEnd.Time}
	if row.StopsAt.Valid {
		sub.StopsAt = &row.StopsAt.Time
	}
	return RecurringDetails{Mode: "test", State: "ready", Subscription: sub}, nil
}

// subscriptionSnapshot is processor state fetched without holding the donor
// lock, with the reconciliation it was read after.
type subscriptionSnapshot struct {
	attempt  pgtype.UUID
	observed pgtype.Timestamptz
	remote   payments.Object
}

// prefetchSubscription reads the donor's latest subscription from the processor
// before any lock is taken, so read-only views do not hold the donor lock
// across a network call.
func (s RecurringService) prefetchSubscription(ctx context.Context, donor pgtype.UUID) (*subscriptionSnapshot, error) {
	q := queries.New(s.DB)
	attempt, err := q.LatestMonthlyAttemptSnapshot(ctx, donor)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !attempt.StripeSubscriptionID.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	observed, err := q.SubscriptionObservedAt(ctx, attempt.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if s.Gateway == nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	remote, err := s.Gateway.RetrieveSubscription(ctx, attempt.StripeSubscriptionID.String)
	if err != nil {
		return nil, err
	}
	return &subscriptionSnapshot{attempt: attempt.ID, observed: observed, remote: remote}, nil
}
func (s RecurringService) donorSnapshot(ctx context.Context, token string) (*subscriptionSnapshot, error) {
	d, err := Authenticated(ctx, queries.New(s.DB), token, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return s.prefetchSubscription(ctx, d.ID)
}

// currentSubscription returns processor state for the locked attempt. A
// snapshot is used only if nothing was reconciled after it was read; otherwise
// it may predate the stored state, so the processor is read again.
func (s RecurringService) currentSubscription(ctx context.Context, q *queries.Queries, attempt queries.DonorStripeCheckout, snapshot *subscriptionSnapshot) (payments.Object, error) {
	if snapshot != nil && snapshot.attempt == attempt.ID {
		observed, err := q.SubscriptionObservedAt(ctx, attempt.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if observed.Valid == snapshot.observed.Valid && observed.Time.Equal(snapshot.observed.Time) {
			return snapshot.remote, nil
		}
	}
	if s.Gateway == nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	return s.Gateway.RetrieveSubscription(ctx, attempt.StripeSubscriptionID.String)
}
func (s RecurringService) read(ctx context.Context, tx pgx.Tx, attempt queries.DonorStripeCheckout, now time.Time, policy string, snapshot *subscriptionSnapshot) (queries.DonorSubscription, payments.Object, error) {
	empty := queries.DonorSubscription{}
	q := queries.New(tx)
	remote, err := s.currentSubscription(ctx, q, attempt, snapshot)
	if err != nil {
		return empty, nil, err
	}
	draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
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
		if _, err = billingAnchor(remote); err != nil {
			return empty, nil, err
		}
	}
	return row, remote, nil
}
func billingAnchor(remote payments.Object) (time.Time, error) {
	anchor, err := remote.Int("billing_cycle_anchor")
	if err != nil || anchor < 0 || anchor > 253402300799 {
		return time.Time{}, payments.ErrFacts
	}
	return time.Unix(anchor, 0).UTC(), nil
}
func (s RecurringService) Details(ctx context.Context, token string) (RecurringDetails, error) {
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return RecurringDetails{}, err
	}
	return ownerTransaction(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (RecurringDetails, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return RecurringDetails{Mode: "test", State: "ready"}, nil
		}
		if err != nil {
			return RecurringDetails{}, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return RecurringDetails{Mode: "test", State: "preparing"}, nil
		}
		row, _, err := s.read(ctx, tx, attempt, now, "", snapshot)
		if err != nil {
			return RecurringDetails{}, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return RecurringDetails{}, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
func (s RecurringService) Cancel(ctx context.Context, token string, request CancellationRequest) (RecurringDetails, error) {
	return s.cancelAuthorized(ctx, donorOwner(token), request)
}
func (s RecurringService) cancelAuthorized(ctx context.Context, owner ownerResolver, r CancellationRequest) (RecurringDetails, error) {
	if !ValidRequestID(r.RequestID) || r.PeriodEnd.IsZero() {
		return RecurringDetails{}, payments.ErrFacts
	}
	_, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (bool, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return false, err
		}
		action, err := q.CancellationByID(ctx, r.RequestID)
		if err == nil {
			if action.DonorID != d.ID || action.AttemptID != attempt.ID || !action.ConfirmedPeriodEnd.Time.Equal(r.PeriodEnd) {
				return false, ErrSubscriptionConflict
			}
			return true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		return true, q.InsertCancellation(ctx, queries.InsertCancellationParams{ID: r.RequestID, DonorID: d.ID, AttemptID: attempt.ID, ConfirmedPeriodEnd: timestamp(r.PeriodEnd), CreatedAt: timestamp(now)})
	})
	if err != nil {
		return RecurringDetails{}, err
	}
	return ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (RecurringDetails, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return RecurringDetails{}, err
		}
		action, err := q.CancellationByID(ctx, r.RequestID)
		if err != nil {
			return RecurringDetails{}, err
		}
		row, _, err := s.read(ctx, tx, attempt, now, "", nil)
		if err != nil {
			return RecurringDetails{}, err
		}
		if !action.CompletedAt.Valid {
			if !row.StopsAt.Valid {
				if !row.PeriodEnd.Time.Equal(r.PeriodEnd) || !now.Before(r.PeriodEnd) || now.Sub(action.CreatedAt.Time) >= 23*time.Hour {
					return RecurringDetails{}, ErrSubscriptionConflict
				}
				switch row.Status {
				case "active", "past_due", "unpaid":
				default:
					return RecurringDetails{}, ErrSubscriptionConflict
				}
				if err = s.Gateway.ScheduleCancellation(ctx, attempt.StripeSubscriptionID.String, r.PeriodEnd.Unix(), "dad-cancel-"+action.ID.String()); err != nil {
					return RecurringDetails{}, err
				}
				row, _, err = s.read(ctx, tx, attempt, now, "", nil)
				if err != nil {
					return RecurringDetails{}, err
				}
				if !row.StopsAt.Valid {
					return RecurringDetails{}, ErrSubscriptionConflict
				}
			}
			if err = q.CompleteCancellation(ctx, queries.CompleteCancellationParams{ID: action.ID, CompletedAt: timestamp(now)}); err != nil {
				return RecurringDetails{}, err
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return RecurringDetails{}, err
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
func pauseView(ctx context.Context, q *queries.Queries, p queries.DonorPause) (PauseView, error) {
	until, err := effectiveResume(ctx, q, p)
	if err != nil {
		return PauseView{}, err
	}
	return PauseView{RequestID: p.ID.String(), Months: p.Months, StartsAt: p.StartsAt.Time, ResumesAt: until, Confirmed: p.ConfirmedAt.Valid}, nil
}
func amountView(c queries.DonorAmountChange) *AmountChange {
	return &AmountChange{RequestID: c.ID.String(), AmountMinor: c.AmountMinor, PreviousMinor: c.PreviousMinor, EffectiveAt: c.EffectiveAt.Time, Confirmed: c.ConfirmedAt.Valid}
}
func resumptionView(c queries.DonorResumption) *Resumption {
	return &Resumption{RequestID: c.ID.String(), PauseID: c.PauseID.String(), NextRenewal: c.NextRenewal.Time, Confirmed: c.ConfirmedAt.Valid}
}
