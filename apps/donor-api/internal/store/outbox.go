package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/recognition"
	"impactgraph/donor-api/internal/store/queries"
)

func queueMessage(ctx context.Context, q *queries.Queries, donor pgtype.UUID, kind, key string, details any, at time.Time) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return q.InsertFinancialMessage(ctx, queries.InsertFinancialMessageParams{ID: id, DonorID: donor, Kind: kind, DedupKey: key, Context: data, AvailableAt: timestamp(at)})
}

// refreshProjection recomputes the donor's recognition and queues the
// messages and push notifications its facts call for, which commit together
// with it. Delivery is a separate worker. It takes the donor lock if the
// caller does not hold it.
func refreshProjection(ctx context.Context, tx pgx.Tx, id pgtype.UUID, now time.Time) (projection, error) {
	q := queries.New(tx)
	result, err := projectFacts(ctx, q, id, now)
	if err != nil {
		return result, err
	}
	if err = q.QueueFinancialIntents(ctx, queries.QueueFinancialIntentsParams{DonorID: id, AsOf: timestamp(now)}); err != nil {
		return result, err
	}
	return result, queuePushIntents(ctx, q, result, now)
}

// RefreshNextProjection refreshes the donor whose projection is due soonest,
// or else one computed under earlier rules. A donor whose facts cannot be
// projected is deferred with backoff and its failure recorded for the monitor,
// so it cannot hold up every other donor.
func RefreshNextProjection(ctx context.Context, pool *pgxpool.Pool, now time.Time) (bool, error) {
	worked := false
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := queries.New(tx)
		id, err := q.DueProjectionDonor(ctx, timestamp(now))
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = q.OutdatedProjectionDonor(ctx, queries.OutdatedProjectionDonorParams{RulesVersion: projectionRules, ComputedAt: timestamp(now)})
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		worked = true
		refreshErr := pgx.BeginFunc(ctx, tx, func(work pgx.Tx) error {
			_, err := refreshProjection(ctx, work, id, now)
			return err
		})
		if refreshErr == nil {
			return nil
		}
		category, ok := projectionFailure(refreshErr)
		if !ok {
			return refreshErr
		}
		return q.DeferProjection(ctx, queries.DeferProjectionParams{DonorID: id, RefreshError: text(category), AsOf: timestamp(now), RulesVersion: projectionRules})
	})
	return worked && err == nil, err
}

// projectionFailure classifies errors that retrying cannot fix until the
// donor's facts change.
func projectionFailure(err error) (string, bool) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, recognition.ErrInvalid), errors.Is(err, payments.ErrFacts):
		return "InvalidFacts", true
	case errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "22"):
		return "DataException", true
	case errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "23"):
		return "ConstraintViolation", true
	}
	return "", false
}

// queueLoginEmail queues a sign-in link for the donor with email, if any,
// returning them to next unless it is empty. The link itself is minted by the
// delivery worker.
func queueLoginEmail(ctx context.Context, q *queries.Queries, email, next string, now time.Time) error {
	donor, err := q.DonorByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	donor, err = q.LockDonor(ctx, donor.ID)
	if err != nil {
		return err
	}
	if donor.Disabled {
		return nil
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	return q.QueueLoginEmail(ctx, queries.QueueLoginEmailParams{ID: id, DonorID: donor.ID, AvailableAt: timestamp(now), ReturnPath: pgtype.Text{String: next, Valid: next != ""}})
}
