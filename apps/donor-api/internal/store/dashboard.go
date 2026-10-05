package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

// DonorDashboard is the web dashboard contract. Fields stay in alphabetical
// order so responses match the original map encoding byte for byte.
type DonorDashboard struct {
	ComputedAt    time.Time        `json:"computed_at"`
	FoundingDonor bool             `json:"founding_donor"`
	Mode          string           `json:"mode"`
	Referrals     *ReferralSummary `json:"referrals"`
	State         string           `json:"state"`
	Streak        *StreakSummary   `json:"streak"`
}
type ReferralSummary struct {
	Currency   string `json:"currency"`
	IRL        int64  `json:"irl"`
	IRLMinor   int64  `json:"irl_minor"`
	Link       int64  `json:"link"`
	LinkMinor  int64  `json:"link_minor"`
	TotalMinor int64  `json:"total_minor"`
}
type StreakSummary struct {
	AtRisk      bool       `json:"at_risk"`
	Current     int32      `json:"current"`
	GraceEndsAt *time.Time `json:"grace_ends_at"`
}

// MarshalJSON reports a dashboard still being prepared by its state alone.
func (d DonorDashboard) MarshalJSON() ([]byte, error) {
	if d.State == "preparing" {
		return json.Marshal(struct {
			Mode  string `json:"mode"`
			State string `json:"state"`
		}{d.Mode, d.State})
	}
	type dashboard DonorDashboard
	return json.Marshal(dashboard(d))
}

// Dashboard reads must share a repeatable snapshot: a worker may update several
// related projections while the three constituent queries execute.
func Dashboard(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) (DonorDashboard, error) {
	row, err := q.Projection(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return DonorDashboard{Mode: "test", State: "preparing"}, nil
	}
	if err != nil {
		return DonorDashboard{}, err
	}
	referred, err := q.ReferralTotals(ctx, queries.ReferralTotalsParams{ReferrerID: id, NextRefreshAt: timestamp(now)})
	if err != nil {
		return DonorDashboard{}, err
	}
	missing, err := q.MissingReferredProjection(ctx, id)
	if err != nil {
		return DonorDashboard{}, err
	}
	linkAmount, linkErr := referred.LinkMinor.Int64Value()
	irlAmount, irlErr := referred.IrlMinor.Int64Value()
	ready := !missing && referred.Ready && linkErr == nil && irlErr == nil && linkAmount.Valid && irlAmount.Valid
	linkMinor, irlMinor := linkAmount.Int64, irlAmount.Int64
	result := DonorDashboard{Mode: "test", State: "ready", ComputedAt: row.ComputedAt.Time, FoundingDonor: row.FoundingAwardedAt.Valid}
	if ready && irlMinor <= math.MaxInt64-linkMinor {
		result.Referrals = &ReferralSummary{Link: referred.LinkCount, IRL: referred.IrlCount, LinkMinor: linkMinor, IRLMinor: irlMinor, TotalMinor: linkMinor + irlMinor, Currency: "USD"}
	}
	if row.NextRefreshAt.Time.After(now) && row.CurrentStreak.Valid {
		result.Streak = &StreakSummary{Current: row.CurrentStreak.Int32, AtRisk: row.AtRisk}
		if row.GraceEndsAt.Valid {
			result.Streak.GraceEndsAt = &row.GraceEndsAt.Time
		}
	}
	return result, nil
}

type DashboardReader struct{ DB *pgxpool.Pool }

func (reader DashboardReader) DonorDashboard(ctx context.Context, token string) (DonorDashboard, error) {
	tx, err := reader.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return DonorDashboard{}, err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	q := queries.New(tx)
	donor, err := Authenticated(ctx, q, token, now)
	if err != nil {
		return DonorDashboard{}, err
	}
	body, err := Dashboard(ctx, q, donor.ID, now)
	if err != nil {
		return DonorDashboard{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DonorDashboard{}, err
	}
	return body, nil
}
