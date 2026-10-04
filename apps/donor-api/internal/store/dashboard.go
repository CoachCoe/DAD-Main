package store

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

// Dashboard reads must share a repeatable snapshot: a worker may update several
// related projections while the three constituent queries execute.
func Dashboard(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) (map[string]interface{}, error) {
	row, err := q.Projection(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]interface{}{"mode": "test", "state": "preparing"}, nil
	}
	if err != nil {
		return nil, err
	}
	referred, err := q.ReferralTotals(ctx, queries.ReferralTotalsParams{ReferrerID: id, NextRefreshAt: timestamp(now)})
	if err != nil {
		return nil, err
	}
	missing, err := q.MissingReferredProjection(ctx, id)
	if err != nil {
		return nil, err
	}
	linkAmount, linkErr := referred.LinkMinor.Int64Value()
	irlAmount, irlErr := referred.IrlMinor.Int64Value()
	ready := !missing && referred.Ready && linkErr == nil && irlErr == nil && linkAmount.Valid && irlAmount.Valid
	linkMinor, irlMinor := linkAmount.Int64, irlAmount.Int64
	result := map[string]interface{}{"mode": "test", "state": "ready", "computed_at": row.ComputedAt.Time, "referrals": nil, "streak": nil, "founding_donor": row.FoundingAwardedAt.Valid}
	if ready && irlMinor <= math.MaxInt64-linkMinor {
		result["referrals"] = map[string]interface{}{"link": referred.LinkCount, "irl": referred.IrlCount, "link_minor": linkMinor, "irl_minor": irlMinor, "total_minor": linkMinor + irlMinor, "currency": "USD"}
	}
	if row.NextRefreshAt.Time.After(now) && row.CurrentStreak.Valid {
		var grace interface{}
		if row.GraceEndsAt.Valid {
			grace = row.GraceEndsAt.Time
		}
		result["streak"] = map[string]interface{}{"current": row.CurrentStreak.Int32, "at_risk": row.AtRisk, "grace_ends_at": grace}
	}
	return result, nil
}

type DashboardReader struct{ DB *pgxpool.Pool }

func (reader DashboardReader) DonorDashboard(ctx context.Context, token string) (map[string]interface{}, error) {
	tx, err := reader.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	q := queries.New(tx)
	donor, err := Authenticated(ctx, q, token, now)
	if err != nil {
		return nil, err
	}
	body, err := Dashboard(ctx, q, donor.ID, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return body, nil
}
