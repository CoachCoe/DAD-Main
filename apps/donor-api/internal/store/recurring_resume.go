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

func (s RecurringService) Resume(ctx context.Context, token string, r ResumptionRequest) (map[string]interface{}, error) {
	if !requestIDValid(r.RequestID) || r.NextRenewal.IsZero() {
		return nil, payments.ErrFacts
	}
	owner := donorOwner(token)
	ready, err := s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
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
			schedule, _, err := s.read(ctx, tx, attempt, now, "pause")
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
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause")
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
	if err != nil || ready != nil {
		return ready, err
	}
	return s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return nil, err
		}
		attempt, err := ownedAttempt(ctx, q, d.ID, pause.AttemptID)
		if err != nil {
			return nil, err
		}
		schedule, _, err := s.read(ctx, tx, attempt, now, "pause")
		if err != nil {
			return nil, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid {
			return nil, ErrSubscriptionConflict
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		if err != nil {
			return nil, err
		}
		if !command.ConfirmedAt.Valid {
			if !schedule.PeriodEnd.Time.Equal(command.NextRenewal.Time) || !now.Before(command.NextRenewal.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return nil, ErrSubscriptionConflict
			}
			// Shorten the voiding window without charging any skipped invoices.
			if err = s.Gateway.PauseCollection(ctx, attempt.StripeSubscriptionID.String, command.NextRenewal.Time.Unix(), "dad-resume-"+command.ID.String()); err != nil {
				return nil, err
			}
			if _, _, err = s.read(ctx, tx, attempt, now, "pause"); err != nil {
				return nil, err
			}
			command, err = q.ResumptionByID(ctx, r.RequestID)
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
		return resumptionView(command), nil
	})
}
