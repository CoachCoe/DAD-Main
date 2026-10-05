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

func (s RecurringService) Pause(ctx context.Context, token string, r PauseRequest) (PauseResult, error) {
	if !ValidRequestID(r.RequestID) || r.Months < 1 || r.Months > 3 || r.StartsAt.IsZero() || r.ResumesAt.IsZero() {
		return PauseResult{}, payments.ErrFacts
	}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return PauseResult{}, err
	}
	owner := donorOwner(token)
	var lease *subscriptionLease
	ready, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (*PauseResult, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		fresh := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !fresh {
			return nil, err
		}
		if !fresh && (command.DonorID != d.ID || command.AttemptID != attempt.ID || command.Months != r.Months || !command.StartsAt.Time.Equal(r.StartsAt) || !command.ResumesAt.Time.Equal(r.ResumesAt)) {
			return nil, ErrSubscriptionConflict
		}
		if fresh {
			prior, err := q.LatestPause(ctx, attempt.ID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				until, err := effectiveResume(ctx, q, prior)
				if err != nil {
					return nil, err
				}
				if !prior.ConfirmedAt.Valid || now.Before(until) {
					return nil, ErrSubscriptionConflict
				}
			}
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", snapshot)
		if err != nil {
			return nil, err
		}
		if fresh {
			anchor, err := billingAnchor(remote)
			if err != nil {
				return nil, err
			}
			until, err := renewalAfter(r.StartsAt, r.Months, anchor)
			if err != nil {
				return nil, err
			}
			if schedule.Status != "active" || schedule.StopsAt.Valid || !absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Equal(r.StartsAt) || !until.Equal(r.ResumesAt) || !now.Before(r.StartsAt) {
				return nil, ErrSubscriptionConflict
			}
			if err = q.InsertPause(ctx, queries.InsertPauseParams{ID: r.RequestID, DonorID: d.ID, AttemptID: attempt.ID, Months: r.Months, StartsAt: timestamp(r.StartsAt), ResumesAt: timestamp(r.ResumesAt), CreatedAt: timestamp(now)}); err != nil {
				return nil, err
			}
		}
		if command, err = q.PauseByID(ctx, r.RequestID); err != nil {
			return nil, err
		}
		if command.ConfirmedAt.Valid {
			result, err := completePause(ctx, tx, d.ID, attempt.ID, command, now)
			return &result, err
		}
		if !now.Before(command.StartsAt.Time) || schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(command.StartsAt.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
			return nil, ErrSubscriptionConflict
		}
		lease, err = leaseSubscription(ctx, q, d.ID, attempt, command.ID, now)
		return nil, err
	})
	if err != nil {
		return PauseResult{}, err
	}
	if ready != nil {
		return *ready, nil
	}
	defer s.release(ctx, lease)
	snapshot, err = s.submit(ctx, lease, func(ctx context.Context) error {
		return s.Gateway.PauseCollection(ctx, lease.subscription, r.ResumesAt.Unix(), "dad-pause-"+r.RequestID.String())
	})
	if err != nil {
		return PauseResult{}, err
	}
	return ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (PauseResult, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return PauseResult{}, err
		}
		if _, _, err = s.read(ctx, tx, attempt, now, "pause", snapshot); err != nil {
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
	})
}
func completePause(ctx context.Context, tx pgx.Tx, donor, attempt pgtype.UUID, command queries.DonorPause, now time.Time) (PauseResult, error) {
	q := queries.New(tx)
	if err := releaseLease(ctx, q, attempt, command.ID); err != nil {
		return PauseResult{}, err
	}
	if _, err := RefreshProjectionInTx(ctx, tx, donor, now); err != nil {
		return PauseResult{}, err
	}
	view, err := pauseView(ctx, q, command)
	if err != nil {
		return PauseResult{}, err
	}
	return PauseResult{Pause: view}, nil
}
