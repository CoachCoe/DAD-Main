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
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return Resumption{}, err
	}
	owner := donorOwner(token)
	var lease *subscriptionLease
	ready, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (*Resumption, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return nil, err
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		fresh := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !fresh {
			return nil, err
		}
		if !fresh && (command.DonorID != d.ID || command.PauseID != pause.ID || !command.NextRenewal.Time.Equal(r.NextRenewal)) {
			return nil, ErrSubscriptionConflict
		}
		if fresh {
			if _, err = q.ResumptionForPause(ctx, pause.ID); err == nil {
				return nil, ErrSubscriptionConflict
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
		if fresh || command.ConfirmedAt.Valid {
			latest, err := q.LatestPause(ctx, pause.AttemptID)
			if err != nil {
				return nil, err
			}
			if latest.ID != pause.ID {
				return nil, ErrSubscriptionConflict
			}
		}
		attempt, err := ownedAttempt(ctx, q, d.ID, pause.AttemptID)
		if err != nil {
			return nil, err
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", snapshot)
		if err != nil {
			return nil, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid {
			return nil, ErrSubscriptionConflict
		}
		if fresh {
			if pause, err = q.PauseByID(ctx, pause.ID); err != nil {
				return nil, err
			}
			if !pause.ConfirmedAt.Valid || absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Equal(r.NextRenewal) || !now.Before(r.NextRenewal) || !r.NextRenewal.Before(pause.ResumesAt.Time) {
				return nil, ErrSubscriptionConflict
			}
			if err = q.InsertResumption(ctx, queries.InsertResumptionParams{ID: r.RequestID, DonorID: d.ID, PauseID: pause.ID, NextRenewal: timestamp(r.NextRenewal), CreatedAt: timestamp(now)}); err != nil {
				return nil, err
			}
		}
		if command, err = q.ResumptionByID(ctx, r.RequestID); err != nil {
			return nil, err
		}
		if command.ConfirmedAt.Valid {
			result, err := completeResumption(ctx, tx, d.ID, attempt.ID, command, now)
			return &result, err
		}
		if !schedule.PeriodEnd.Time.Equal(command.NextRenewal.Time) || !now.Before(command.NextRenewal.Time) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
			return nil, ErrSubscriptionConflict
		}
		lease, err = leaseSubscription(ctx, q, d.ID, attempt, command.ID, now)
		return nil, err
	})
	if err != nil {
		return Resumption{}, err
	}
	if ready != nil {
		return *ready, nil
	}
	defer s.release(ctx, lease)
	// Shorten the voiding window without charging any skipped invoices.
	snapshot, err = s.submit(ctx, lease, func(ctx context.Context) error {
		return s.Gateway.PauseCollection(ctx, lease.subscription, r.NextRenewal.Unix(), "dad-resume-"+r.RequestID.String())
	})
	if err != nil {
		return Resumption{}, err
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
		schedule, _, err := s.read(ctx, tx, attempt, now, "pause", snapshot)
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
			return Resumption{}, ErrSubscriptionConflict
		}
		return completeResumption(ctx, tx, d.ID, attempt.ID, command, now)
	})
}
func completeResumption(ctx context.Context, tx pgx.Tx, donor, attempt pgtype.UUID, command queries.DonorResumption, now time.Time) (Resumption, error) {
	if err := releaseLease(ctx, queries.New(tx), attempt, command.ID); err != nil {
		return Resumption{}, err
	}
	if _, err := RefreshProjectionInTx(ctx, tx, donor, now); err != nil {
		return Resumption{}, err
	}
	return *resumptionView(command), nil
}
