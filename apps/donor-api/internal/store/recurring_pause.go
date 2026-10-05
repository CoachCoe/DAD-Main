package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func (s RecurringService) Pause(ctx context.Context, token string, r PauseRequest) (map[string]interface{}, error) {
	if !ValidRequestID(r.RequestID) || r.Months < 1 || r.Months > 3 || r.StartsAt.IsZero() || r.ResumesAt.IsZero() {
		return nil, payments.ErrFacts
	}
	owner := donorOwner(token)
	_, err := s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		if err == nil {
			if command.DonorID != d.ID || command.AttemptID != attempt.ID || command.Months != r.Months || !command.StartsAt.Time.Equal(r.StartsAt) || !command.ResumesAt.Time.Equal(r.ResumesAt) {
				return nil, ErrSubscriptionConflict
			}
			return nil, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
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
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause")
		if err != nil {
			return nil, err
		}
		anchor, _ := remote.Int("billing_cycle_anchor")
		until, err := renewalAfter(r.StartsAt, r.Months, time.Unix(anchor, 0))
		if err != nil {
			return nil, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid || !absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Equal(r.StartsAt) || !until.Equal(r.ResumesAt) || !now.Before(r.StartsAt) {
			return nil, ErrSubscriptionConflict
		}
		return nil, q.InsertPause(ctx, queries.InsertPauseParams{ID: r.RequestID, DonorID: d.ID, AttemptID: attempt.ID, Months: r.Months, StartsAt: timestamp(r.StartsAt), ResumesAt: timestamp(r.ResumesAt), CreatedAt: timestamp(now)})
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
		schedule, _, err := s.read(ctx, tx, attempt, now, "pause")
		if err != nil {
			return nil, err
		}
		command, err := q.PauseByID(ctx, r.RequestID)
		if err != nil {
			return nil, err
		}
		if !command.ConfirmedAt.Valid {
			if !now.Before(command.StartsAt.Time) || schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(command.StartsAt.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return nil, ErrSubscriptionConflict
			}
			if err = s.Gateway.PauseCollection(ctx, attempt.StripeSubscriptionID.String, command.ResumesAt.Time.Unix(), "dad-pause-"+command.ID.String()); err != nil {
				return nil, err
			}
			if _, _, err = s.read(ctx, tx, attempt, now, "pause"); err != nil {
				return nil, err
			}
			command, err = q.PauseByID(ctx, r.RequestID)
			if err != nil {
				return nil, err
			}
			if !command.ConfirmedAt.Valid {
				return nil, ErrSubscriptionConflict
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return nil, err
		}
		view, err := pauseView(ctx, q, command)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"pause": view}, nil
	})
}
