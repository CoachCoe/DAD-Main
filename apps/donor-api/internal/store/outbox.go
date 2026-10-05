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

// QueueFinancialMessages requires the donor lock. Recognition and notification
// intents commit together, while external delivery remains a separate worker.
func QueueFinancialMessages(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) error {
	return q.QueueFinancialIntents(ctx, queries.QueueFinancialIntentsParams{DonorID: id, AsOf: timestamp(now)})
}

func RefreshProjectionInTx(ctx context.Context, tx pgx.Tx, id pgtype.UUID, now time.Time) (queries.SaveProjectionParams, error) {
	result, err := refreshProjectionFactsInTx(ctx, tx, id, now)
	if err != nil {
		return result, err
	}
	return result, QueueFinancialMessages(ctx, queries.New(tx), id, now)
}

// RefreshNextProjection refreshes the donor whose projection is due soonest.
// A donor whose facts cannot be projected is deferred with backoff and its
// failure recorded for the monitor, so it cannot hold up every other donor.
func RefreshNextProjection(ctx context.Context, pool *pgxpool.Pool, now time.Time) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	id, err := q.DueProjectionDonor(ctx, timestamp(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	_, refreshErr := RefreshProjectionInTx(ctx, work, id, now)
	if refreshErr == nil {
		refreshErr = work.Commit(ctx)
	}
	if refreshErr != nil {
		category, ok := projectionFailure(refreshErr)
		if !ok {
			return false, refreshErr
		}
		if err = work.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return false, err
		}
		if err = q.DeferProjection(ctx, queries.DeferProjectionParams{DonorID: id, RefreshError: pgtype.Text{String: category, Valid: true}, AsOf: timestamp(now)}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
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

func QueueLoginEmailInTx(ctx context.Context, tx pgx.Tx, email string, now time.Time) error {
	q := queries.New(tx)
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
	return q.QueueLoginEmail(ctx, queries.QueueLoginEmailParams{ID: id, DonorID: donor.ID, AvailableAt: timestamp(now)})
}
