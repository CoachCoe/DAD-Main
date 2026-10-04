package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

func queueMessage(ctx context.Context, q *queries.Queries, donor pgtype.UUID, kind, key string, details interface{}, at time.Time) error {
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
	if _, err = RefreshProjectionInTx(ctx, tx, id, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
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
