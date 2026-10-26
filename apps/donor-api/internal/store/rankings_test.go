package store

import (
	"context"
	"testing"

	"dad/donor-api/internal/dbroles"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rankDonor seeds a donor with a projection carrying a streak and start, and an
// optional city + opt-out, for ranking tests.
func rankDonor(t *testing.T, pool *pgxpool.Pool, streak int32, since time.Time, city string, optedOut bool) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	id, _ := donor(t, pool)
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DELETE FROM donor_rankings WHERE donor_id=$1", id)
		pool.Exec(context.Background(), "DELETE FROM donor_preferences WHERE donor_id=$1", id)
	})
	if city != "" {
		if _, err := pool.Exec(ctx, "UPDATE donors SET city=$2 WHERE id=$1", id, city); err != nil {
			t.Fatal(err)
		}
	}
	pid, _ := NewID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO donor_metric_projections (id,donor_id,best_streak,current_streak,streak_since,computed_at,next_refresh_at)
		 VALUES ($1,$2,$3,$3,$4,$5,$6)`,
		pid, id, streak, since, time.Now().UTC(), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if optedOut {
		prefID, _ := NewID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO donor_preferences (id,donor_id,version,referral_emails,marketing_emails,rankings_opted_out)
			 VALUES ($1,$2,1,true,true,true)`, prefID, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestRankingRefreshPercentileCityRankAndOptOut(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	now := time.Now().UTC()
	base := now.Add(-100 * 24 * time.Hour)

	// Clean any pre-existing ranking rows for isolation.
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM donor_rankings") })
	if _, err := pool.Exec(ctx, "DELETE FROM donor_rankings"); err != nil {
		t.Fatal(err)
	}

	// Top streak in city A, with a tie broken by earlier start.
	top := rankDonor(t, pool, 500, base, "Austin", false)
	tieEarlier := rankDonor(t, pool, 300, base.Add(-10*24*time.Hour), "Austin", false)
	tieLater := rankDonor(t, pool, 300, base, "Austin", false)
	_ = rankDonor(t, pool, 100, base, "Austin", false)
	// An opted-out donor with the highest streak must be excluded entirely.
	optedOut := rankDonor(t, pool, 9999, base, "Austin", true)

	cleanup(t, "DELETE FROM donor_badges WHERE code IN ('NAT2','CITY50')")
	svc := RankingService{DB: as(t, dbroles.ProjectionWorker)}
	if err := svc.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	held := func(id pgtype.UUID) string {
		t.Helper()
		var codes string
		if err := pool.QueryRow(ctx, "SELECT coalesce(string_agg(code, ',' ORDER BY code), '') FROM donor_badges WHERE donor_id=$1", id).Scan(&codes); err != nil {
			t.Fatal(err)
		}
		return codes
	}
	if got := held(top); got != "CITY50,NAT2" {
		t.Fatalf("top donor holds %q after the rollup, want Top 50 City and Top 2%%", got)
	}
	if got := held(optedOut); got != "" {
		t.Fatalf("opted-out donor earned ranking badges %q", got)
	}

	cityRank := func(id pgtype.UUID) int {
		t.Helper()
		var r pgtype.Int4
		if err := pool.QueryRow(ctx, "SELECT city_rank FROM donor_rankings WHERE donor_id=$1", id).Scan(&r); err != nil {
			t.Fatalf("no ranking row for donor: %v", err)
		}
		return int(r.Int32)
	}

	// Top streak ranks #1 in the city.
	if got := cityRank(top); got != 1 {
		t.Fatalf("top donor city_rank=%d want 1", got)
	}
	// Tie on streak 300: the earlier start ranks ahead (rank 2 vs 3).
	if e, l := cityRank(tieEarlier), cityRank(tieLater); !(e < l) {
		t.Fatalf("tie-break failed: earlier=%d later=%d (earlier should rank ahead)", e, l)
	}

	// The opted-out donor has no ranking row at all.
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM donor_rankings WHERE donor_id=$1)", optedOut).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("opted-out donor was ranked")
	}

	// The top donor's national percentile is the best bucket (1).
	var pct int
	if err := pool.QueryRow(ctx, "SELECT national_percentile FROM donor_rankings WHERE donor_id=$1", top).Scan(&pct); err != nil {
		t.Fatal(err)
	}
	if pct < 1 || pct > 100 {
		t.Fatalf("percentile out of range: %d", pct)
	}
}
