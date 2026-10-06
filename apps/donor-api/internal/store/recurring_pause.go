package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

// Pause skips 1 to 3 renewals from the next one; the donor confirmed the
// exact resumption date offered by PauseOptions.
func (s RecurringService) Pause(ctx context.Context, token string, r PauseRequest) (PauseResult, error) {
	if !ValidRequestID(r.RequestID) || r.Months < 1 || r.Months > 3 || r.StartsAt.IsZero() || r.ResumesAt.IsZero() {
		return PauseResult{}, payments.ErrFacts
	}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return PauseResult{}, err
	}
	prepare := func(tx pgx.Tx, d queries.Donor, now time.Time) (*PauseResult, *subscriptionLease, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, nil, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		fresh := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !fresh {
			return nil, nil, err
		}
		if !fresh && (command.DonorID != d.ID || command.AttemptID != attempt.ID || command.Months != r.Months || !command.StartsAt.Time.Equal(r.StartsAt) || !command.ResumesAt.Time.Equal(r.ResumesAt)) {
			return nil, nil, ErrSubscriptionConflict
		}
		if fresh {
			if err = requireNoActivePause(ctx, q, attempt.ID, now); err != nil {
				return nil, nil, err
			}
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, forPause, snapshot)
		if err != nil {
			return nil, nil, err
		}
		if fresh {
			if err = insertPause(ctx, q, d.ID, attempt.ID, schedule, remote, r, now); err != nil {
				return nil, nil, err
			}
		}
		if command, err = q.PauseByID(ctx, r.RequestID); err != nil {
			return nil, nil, err
		}
		if command.ConfirmedAt.Valid {
			result, err := completePause(ctx, tx, d.ID, attempt.ID, command, now)
			return &result, nil, err
		}
		if !now.Before(command.StartsAt.Time) || schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(command.StartsAt.Time) || now.Sub(command.CreatedAt.Time) >= commandRetryWindow {
			return nil, nil, ErrSubscriptionConflict
		}
		lease, err := leaseSubscription(ctx, q, d.ID, attempt, command.ID, now)
		return nil, lease, err
	}
	mutate := func(ctx context.Context, lease *subscriptionLease) error {
		return s.Gateway.PauseCollection(ctx, lease.subscription, r.ResumesAt.Unix(), "dad-pause-"+r.RequestID.String())
	}
	complete := func(tx pgx.Tx, d queries.Donor, now time.Time, snapshot *subscriptionSnapshot) (PauseResult, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return PauseResult{}, err
		}
		if _, _, err = s.read(ctx, tx, attempt, now, forPause, snapshot); err != nil {
			return PauseResult{}, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		if err != nil {
			return PauseResult{}, err
		}
		if !command.ConfirmedAt.Valid {
			return PauseResult{}, ErrSubscriptionConflict
		}
		return completePause(ctx, tx, d.ID, attempt.ID, command, now)
	}
	return leasedCommand(ctx, s, donorOwner(token), prepare, mutate, complete)
}

// requireNoActivePause refuses a new pause while an earlier one is unconfirmed
// or has not yet resumed.
func requireNoActivePause(ctx context.Context, q *queries.Queries, attempt pgtype.UUID, now time.Time) error {
	prior, err := q.LatestPause(ctx, attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	until, err := effectiveResume(ctx, q, prior)
	if err != nil {
		return err
	}
	if !prior.ConfirmedAt.Valid || now.Before(until) {
		return ErrSubscriptionConflict
	}
	return nil
}

// insertPause records r once the subscription shows it is still the pause
// that was offered: from the next renewal, resuming the chosen months later.
func insertPause(ctx context.Context, q *queries.Queries, donor, attempt pgtype.UUID, schedule queries.DonorSubscription, remote payments.Object, r PauseRequest, now time.Time) error {
	anchor, err := remote.Unix("billing_cycle_anchor")
	if err != nil {
		return err
	}
	until, err := renewalAfter(r.StartsAt, r.Months, anchor)
	if err != nil {
		return err
	}
	if schedule.Status != "active" || schedule.StopsAt.Valid || !remote.Absent("pause_collection") || !schedule.PeriodEnd.Time.Equal(r.StartsAt) || !until.Equal(r.ResumesAt) || !now.Before(r.StartsAt) {
		return ErrSubscriptionConflict
	}
	return q.InsertPause(ctx, queries.InsertPauseParams{ID: r.RequestID, DonorID: donor, AttemptID: attempt, Months: r.Months, StartsAt: timestamp(r.StartsAt), ResumesAt: timestamp(r.ResumesAt), CreatedAt: timestamp(now)})
}

func completePause(ctx context.Context, tx pgx.Tx, donor, attempt pgtype.UUID, command queries.DonorPause, now time.Time) (PauseResult, error) {
	q := queries.New(tx)
	if err := releaseLease(ctx, q, attempt, command.ID); err != nil {
		return PauseResult{}, err
	}
	if _, err := refreshProjection(ctx, tx, donor, now); err != nil {
		return PauseResult{}, err
	}
	view, err := pauseView(ctx, q, command)
	if err != nil {
		return PauseResult{}, err
	}
	return PauseResult{Pause: view}, nil
}
