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

// Resume ends a confirmed pause early, at the next renewal. The processor's
// voiding window is shortened rather than lifted, so skipped invoices are
// never charged.
func (s RecurringService) Resume(ctx context.Context, token string, r ResumptionRequest) (Resumption, error) {
	if !ValidRequestID(r.RequestID) || r.NextRenewal.IsZero() {
		return Resumption{}, payments.ErrFacts
	}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return Resumption{}, err
	}
	prepare := func(tx pgx.Tx, d queries.Donor, now time.Time) (*Resumption, *subscriptionLease, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return nil, nil, err
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		fresh := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !fresh {
			return nil, nil, err
		}
		if !fresh && (command.DonorID != d.ID || command.PauseID != pause.ID || !command.NextRenewal.Time.Equal(r.NextRenewal)) {
			return nil, nil, ErrSubscriptionConflict
		}
		if fresh || command.ConfirmedAt.Valid {
			if err = requireResumable(ctx, q, pause, fresh); err != nil {
				return nil, nil, err
			}
		}
		attempt, schedule, remote, err := s.readResumable(ctx, tx, d.ID, pause, now, snapshot)
		if err != nil {
			return nil, nil, err
		}
		if fresh {
			if err = insertResumption(ctx, q, d.ID, pause.ID, schedule, remote, r, now); err != nil {
				return nil, nil, err
			}
		}
		if command, err = q.ResumptionByID(ctx, r.RequestID); err != nil {
			return nil, nil, err
		}
		if command.ConfirmedAt.Valid {
			result, err := completeResumption(ctx, tx, d.ID, attempt.ID, command, now)
			return &result, nil, err
		}
		if !schedule.PeriodEnd.Time.Equal(command.NextRenewal.Time) || !now.Before(command.NextRenewal.Time) || now.Sub(command.CreatedAt.Time) >= commandRetryWindow {
			return nil, nil, ErrSubscriptionConflict
		}
		lease, err := leaseSubscription(ctx, q, d.ID, attempt, command.ID, now)
		return nil, lease, err
	}
	mutate := func(ctx context.Context, lease *subscriptionLease) error {
		return s.Gateway.PauseCollection(ctx, lease.subscription, r.NextRenewal.Unix(), "dad-resume-"+r.RequestID.String())
	}
	complete := func(tx pgx.Tx, d queries.Donor, now time.Time, snapshot *subscriptionSnapshot) (Resumption, error) {
		q := queries.New(tx)
		pause, err := ownedPause(ctx, q, d.ID, r.PauseID)
		if err != nil {
			return Resumption{}, err
		}
		attempt, _, _, err := s.readResumable(ctx, tx, d.ID, pause, now, snapshot)
		if err != nil {
			return Resumption{}, err
		}
		command, err := q.ResumptionByID(ctx, r.RequestID)
		if err != nil {
			return Resumption{}, err
		}
		if !command.ConfirmedAt.Valid {
			return Resumption{}, ErrSubscriptionConflict
		}
		return completeResumption(ctx, tx, d.ID, attempt.ID, command, now)
	}
	return leasedCommand(ctx, s, donorOwner(token), prepare, mutate, complete)
}

// requireResumable refuses to resume a pause that is no longer its
// subscription's latest, or, for a new request, one already being resumed.
func requireResumable(ctx context.Context, q *queries.Queries, pause queries.DonorPause, fresh bool) error {
	if fresh {
		_, err := q.ResumptionForPause(ctx, pause.ID)
		if err == nil {
			return ErrSubscriptionConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	latest, err := q.LatestPause(ctx, pause.AttemptID)
	if err != nil {
		return err
	}
	if latest.ID != pause.ID {
		return ErrSubscriptionConflict
	}
	return nil
}

// insertResumption records r once the processor shows the confirmed pause
// still voiding renewals and r.NextRenewal is the next renewal it would skip.
func insertResumption(ctx context.Context, q *queries.Queries, donor, pauseID pgtype.UUID, schedule queries.DonorSubscription, remote payments.Object, r ResumptionRequest, now time.Time) error {
	pause, err := q.PauseByID(ctx, pauseID)
	if err != nil {
		return err
	}
	if !pause.ConfirmedAt.Valid || remote.Absent("pause_collection") || !schedule.PeriodEnd.Time.Equal(r.NextRenewal) || !now.Before(r.NextRenewal) || !r.NextRenewal.Before(pause.ResumesAt.Time) {
		return ErrSubscriptionConflict
	}
	return q.InsertResumption(ctx, queries.InsertResumptionParams{ID: r.RequestID, DonorID: donor, PauseID: pause.ID, NextRenewal: timestamp(r.NextRenewal), CreatedAt: timestamp(now)})
}

// readResumable reads the paused subscription, which must still be active and
// renewing for an early resumption to apply.
func (s RecurringService) readResumable(ctx context.Context, tx pgx.Tx, donor pgtype.UUID, pause queries.DonorPause, now time.Time, snapshot *subscriptionSnapshot) (queries.DonorStripeCheckout, queries.DonorSubscription, payments.Object, error) {
	attempt, err := ownedAttempt(ctx, queries.New(tx), donor, pause.AttemptID)
	if err != nil {
		return attempt, queries.DonorSubscription{}, nil, err
	}
	schedule, remote, err := s.read(ctx, tx, attempt, now, forPause, snapshot)
	if err != nil {
		return attempt, schedule, nil, err
	}
	if schedule.Status != "active" || schedule.StopsAt.Valid {
		return attempt, schedule, nil, ErrSubscriptionConflict
	}
	return attempt, schedule, remote, nil
}

func completeResumption(ctx context.Context, tx pgx.Tx, donor, attempt pgtype.UUID, command queries.DonorResumption, now time.Time) (Resumption, error) {
	if err := releaseLease(ctx, queries.New(tx), attempt, command.ID); err != nil {
		return Resumption{}, err
	}
	if _, err := refreshProjection(ctx, tx, donor, now); err != nil {
		return Resumption{}, err
	}
	return resumptionView(command), nil
}
