package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func (s RecurringService) Pause(ctx context.Context, token string, r PauseRequest) (PauseResult, error) {
	if !ValidRequestID(r.RequestID) || r.Months < 1 || r.Months > 3 || r.StartsAt.IsZero() || r.ResumesAt.IsZero() {
		return PauseResult{}, payments.ErrFacts
	}
	owner := donorOwner(token)
	_, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (bool, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return false, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		if err == nil {
			if command.DonorID != d.ID || command.AttemptID != attempt.ID || command.Months != r.Months || !command.StartsAt.Time.Equal(r.StartsAt) || !command.ResumesAt.Time.Equal(r.ResumesAt) {
				return false, ErrSubscriptionConflict
			}
			return true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		prior, err := q.LatestPause(ctx, attempt.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		if err == nil {
			until, err := effectiveResume(ctx, q, prior)
			if err != nil {
				return false, err
			}
			if !prior.ConfirmedAt.Valid || now.Before(until) {
				return false, ErrSubscriptionConflict
			}
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", nil)
		if err != nil {
			return false, err
		}
		anchor, err := billingAnchor(remote)
		if err != nil {
			return false, err
		}
		until, err := renewalAfter(r.StartsAt, r.Months, anchor)
		if err != nil {
			return false, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid || !absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Equal(r.StartsAt) || !until.Equal(r.ResumesAt) || !now.Before(r.StartsAt) {
			return false, ErrSubscriptionConflict
		}
		return true, q.InsertPause(ctx, queries.InsertPauseParams{ID: r.RequestID, DonorID: d.ID, AttemptID: attempt.ID, Months: r.Months, StartsAt: timestamp(r.StartsAt), ResumesAt: timestamp(r.ResumesAt), CreatedAt: timestamp(now)})
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
		schedule, _, err := s.read(ctx, tx, attempt, now, "pause", nil)
		if err != nil {
			return PauseResult{}, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		if err != nil {
			return PauseResult{}, err
		}
		if !command.ConfirmedAt.Valid {
			if !now.Before(command.StartsAt.Time) || schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(command.StartsAt.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return PauseResult{}, ErrSubscriptionConflict
			}
			if err = s.Gateway.PauseCollection(ctx, attempt.StripeSubscriptionID.String, command.ResumesAt.Time.Unix(), "dad-pause-"+command.ID.String()); err != nil {
				return PauseResult{}, err
			}
			if _, _, err = s.read(ctx, tx, attempt, now, "pause", nil); err != nil {
				return PauseResult{}, err
			}
			command, err = q.PauseByID(ctx, r.RequestID)
			if err != nil {
				return PauseResult{}, err
			}
			if !command.ConfirmedAt.Valid {
				return PauseResult{}, ErrSubscriptionConflict
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return PauseResult{}, err
		}
		view, err := pauseView(ctx, q, command)
		if err != nil {
			return PauseResult{}, err
		}
		return PauseResult{Pause: view}, nil
	})
}
