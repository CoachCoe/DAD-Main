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

func ownedPause(ctx context.Context, q *queries.Queries, donor, id pgtype.UUID) (queries.DonorPause, error) {
	pause, err := q.PauseByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && pause.DonorID != donor) {
		return pause, ErrSubscriptionConflict
	}
	return pause, err
}

func (s RecurringService) Resume(ctx context.Context, token string, r ResumptionRequest) (Resumption, error) {
	if !ValidRequestID(r.RequestID) || r.NextRenewal.IsZero() {
		return Resumption{}, payments.ErrFacts
	}
	owner := donorOwner(token)
	ready, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (*Resumption, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return nil, err
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		if err == nil {
			if command.DonorID != d.ID || command.PauseID != pause.ID || !command.NextRenewal.Time.Equal(r.NextRenewal) {
				return nil, ErrSubscriptionConflict
			}
			if !command.ConfirmedAt.Valid {
				return nil, nil
			}
			latest, err := q.LatestPause(ctx, pause.AttemptID)
			if err != nil {
				return nil, err
			}
			attempt, err := ownedAttempt(ctx, q, d.ID, pause.AttemptID)
			if err != nil {
				return nil, err
			}
			schedule, _, err := s.read(ctx, tx, attempt, now, "pause", nil)
			if err != nil {
				return nil, err
			}
			if latest.ID != pause.ID || schedule.StopsAt.Valid || schedule.Status != "active" {
				return nil, ErrSubscriptionConflict
			}
			return resumptionView(command), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if _, err = q.ResumptionForPause(ctx, pause.ID); err == nil {
			return nil, ErrSubscriptionConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		latest, err := q.LatestPause(ctx, pause.AttemptID)
		if err != nil {
			return nil, err
		}
		if latest.ID != pause.ID {
			return nil, ErrSubscriptionConflict
		}
		attempt, err := ownedAttempt(ctx, q, d.ID, pause.AttemptID)
		if err != nil {
			return nil, err
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", nil)
		if err != nil {
			return nil, err
		}
		pause, err = q.PauseByID(ctx, pause.ID)
		if err != nil {
			return nil, err
		}
		if !pause.ConfirmedAt.Valid || absent(remote, "pause_collection") || schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(r.NextRenewal) || !now.Before(r.NextRenewal) || !r.NextRenewal.Before(pause.ResumesAt.Time) {
			return nil, ErrSubscriptionConflict
		}
		return nil, q.InsertResumption(ctx, queries.InsertResumptionParams{ID: r.RequestID, DonorID: d.ID, PauseID: pause.ID, NextRenewal: timestamp(r.NextRenewal), CreatedAt: timestamp(now)})
	})
	if err != nil {
		return Resumption{}, err
	}
	if ready != nil {
		return *ready, nil
	}
	return ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (Resumption, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return Resumption{}, err
		}
		attempt, err := ownedAttempt(ctx, q, d.ID, pause.AttemptID)
		if err != nil {
			return Resumption{}, err
		}
		schedule, _, err := s.read(ctx, tx, attempt, now, "pause", nil)
		if err != nil {
			return Resumption{}, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid {
			return Resumption{}, ErrSubscriptionConflict
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		if err != nil {
			return Resumption{}, err
		}
		if !command.ConfirmedAt.Valid {
			if !schedule.PeriodEnd.Time.Equal(command.NextRenewal.Time) || !now.Before(command.NextRenewal.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return Resumption{}, ErrSubscriptionConflict
			}
			// Shorten the voiding window without charging any skipped invoices.
			if err = s.Gateway.PauseCollection(ctx, attempt.StripeSubscriptionID.String, command.NextRenewal.Time.Unix(), "dad-resume-"+command.ID.String()); err != nil {
				return Resumption{}, err
			}
			if _, _, err = s.read(ctx, tx, attempt, now, "pause", nil); err != nil {
				return Resumption{}, err
			}
			command, err = q.ResumptionByID(ctx, r.RequestID)
			if err != nil {
				return Resumption{}, err
			}
			if !command.ConfirmedAt.Valid {
				return Resumption{}, ErrSubscriptionConflict
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return Resumption{}, err
		}
		return *resumptionView(command), nil
	})
}
