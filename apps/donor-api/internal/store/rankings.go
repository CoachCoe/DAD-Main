package store

import (
	"context"
	"errors"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RankingService owns the periodic ranking rollup (PRD §6.4, Phase 3.3). Refresh
// recomputes the whole population in one set-based pass on a schedule; the donor
// read is a single-row lookup. This keeps ranking off the per-request path at 1M
// donors (risk #6).
type RankingService struct{ DB *pgxpool.Pool }

// Ranking is a donor's own position (K-3: never anyone else's, never a list).
type Ranking struct {
	NationalPercentile int       `json:"national_percentile"`
	City               *string   `json:"city"`
	CityRank           *int      `json:"city_rank"`
	ComputedAt         time.Time `json:"computed_at"`
}

// Refresh recomputes every eligible donor's rank and prunes donors who dropped
// out of the population (opted out, lapsed, disabled) since the last run. It
// runs in one transaction so readers never see a half-rebuilt snapshot.
func (s RankingService) Refresh(ctx context.Context, now time.Time) error {
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := q.RefreshRankings(ctx, timestamp(now)); err != nil {
			return err
		}
		// Rows not touched by this refresh (older computed_at) are no longer
		// eligible; drop them so a lapsed/opted-out donor stops being ranked.
		return q.PruneStaleRankings(ctx, timestamp(now))
	})
}

// ForDonor returns a donor's own ranking, or (nil, nil) when they are unranked
// (no streak, opted out, or not yet computed).
func ForDonor(ctx context.Context, q *queries.Queries, donorID pgtype.UUID) (*Ranking, error) {
	row, err := q.DonorRanking(ctx, donorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := &Ranking{NationalPercentile: int(row.NationalPercentile), ComputedAt: row.ComputedAt.Time}
	if row.City.Valid {
		city := row.City.String
		r.City = &city
	}
	if row.CityRank.Valid {
		rank := int(row.CityRank.Int32)
		r.CityRank = &rank
	}
	return r, nil
}

// RankingInterval is how often the global ranking rollup is recomputed. Ranking
// is a periodic projection, not a per-request or per-tick computation (risk #6),
// so the projections worker refreshes it at most this often.
const RankingInterval = 15 * time.Minute

// RefreshIfDue recomputes the rankings only when the last computation is older
// than RankingInterval, so it rides the projections worker loop cheaply. It
// returns whether it refreshed.
func (s RankingService) RefreshIfDue(ctx context.Context, now time.Time) (bool, error) {
	last, err := queries.New(s.DB).LastRankingRefresh(ctx)
	if err != nil {
		return false, err
	}
	if last.Valid && now.Sub(last.Time) < RankingInterval {
		return false, nil
	}
	return true, s.Refresh(ctx, now)
}

// Ranking returns the signed-in donor's own ranking.
func (s RankingService) Ranking(ctx context.Context, token string) (*Ranking, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, _ time.Time) (*Ranking, error) {
		return ForDonor(ctx, q, d.ID)
	})
}
