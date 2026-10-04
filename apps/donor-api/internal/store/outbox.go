package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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
	gifts, err := q.GiftHistory(ctx, id)
	if err != nil {
		return err
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		return err
	}
	years := map[int]bool{}
	for _, g := range gifts {
		if g.PaidAt.Time.After(now) {
			continue
		}
		if err = queueMessage(ctx, q, id, "RECEIPT", "receipt:"+g.ID.String(), map[string]string{"gift_id": g.ID.String()}, now); err != nil {
			return err
		}
		year := g.PaidAt.Time.In(zone).Year()
		if year >= 2026 && year < now.In(zone).Year() {
			years[year] = true
		}
	}
	for year := range years {
		if err = queueMessage(ctx, q, id, "ANNUAL", "annual:"+id.String()+":"+strconv.Itoa(year), map[string]int{"year": year}, now); err != nil {
			return err
		}
	}
	projection, err := q.Projection(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && projection.ReferrerID.Valid && projection.NetMinor.Valid && projection.NetMinor.Int64 > 0 {
		if err = queueMessage(ctx, q, projection.ReferrerID, "REFERRAL", "referral:"+id.String(), map[string]string{"referred_id": id.String(), "kind": projection.ReferralKind.String}, now); err != nil {
			return err
		}
	}
	periods, err := q.PeriodHistory(ctx, id)
	if err != nil {
		return err
	}
	for _, p := range periods {
		if p.Status == "FAILED" {
			for _, day := range []int{0, 7, 13} {
				if err = queueMessage(ctx, q, id, "RECOVERY", "recovery:"+p.ID.String()+":"+strconv.Itoa(day), map[string]interface{}{"period_id": p.ID.String(), "day": day}, p.DueAt.Time.Add(time.Duration(day)*24*time.Hour)); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
