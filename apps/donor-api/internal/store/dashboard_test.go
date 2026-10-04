package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

func TestDashboardAggregateNeverInventsTotalsFromIncompleteOrOverflowingFacts(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	owner, first, second := testID(t), testID(t), testID(t)
	for _, id := range []pgtype.UUID{owner, first, second} {
		mustExec(t, tx, "INSERT INTO donors(id,email) VALUES($1,$2)", id, id.String()+"@example.org")
		mustExec(t, tx, "INSERT INTO donor_metric_projections(id,donor_id,net_minor,current_streak,computed_at,next_refresh_at) VALUES($1,$2,0,0,$3,$4)", testID(t), id, now, now.Add(time.Hour))
	}
	mustExec(t, tx, "UPDATE donor_metric_projections SET referrer_id=$1,referral_kind='LINK',net_minor=3000 WHERE donor_id=$2", owner, first)
	mustExec(t, tx, "UPDATE donor_metric_projections SET referrer_id=$1,referral_kind='IRL',net_minor=9000 WHERE donor_id=$2", owner, second)
	q := queries.New(tx)
	result, err := Dashboard(ctx, q, owner, now)
	if err != nil {
		t.Fatal(err)
	}
	referrals := result["referrals"].(map[string]interface{})
	if referrals["total_minor"] != int64(12000) || referrals["link"] != int64(1) || referrals["irl"] != int64(1) {
		t.Fatal("wrong split", referrals)
	}
	for _, modification := range []string{
		"net_minor=NULL", "next_refresh_at=computed_at", "referral_kind=NULL",
	} {
		mustExec(t, tx, "SAVEPOINT invalid_projection")
		mustExec(t, tx, "UPDATE donor_metric_projections SET "+modification+" WHERE donor_id=$1", first)
		result, err = Dashboard(ctx, q, owner, now)
		if err != nil || result["referrals"] != nil {
			t.Fatal("incomplete aggregate exposed", modification, result, err)
		}
		mustExec(t, tx, "ROLLBACK TO SAVEPOINT invalid_projection")
	}
	for _, sameChannel := range []bool{false, true} {
		mustExec(t, tx, "UPDATE donor_metric_projections SET net_minor=$1 WHERE donor_id=$2", int64(math.MaxInt64), first)
		if sameChannel {
			mustExec(t, tx, "UPDATE donor_metric_projections SET referral_kind='LINK' WHERE donor_id=$1", second)
		}
		result, err = Dashboard(ctx, q, owner, now)
		if err != nil || result["referrals"] != nil {
			t.Fatal("overflowing aggregate exposed", result, err)
		}
	}
}
