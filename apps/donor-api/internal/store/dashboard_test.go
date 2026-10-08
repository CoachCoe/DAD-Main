package store

import (
	"context"
	"encoding/json"
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
	result, err := dashboard(ctx, q, owner, now)
	if err != nil {
		t.Fatal(err)
	}
	referrals := result.Referrals
	if referrals == nil || referrals.TotalMinor != 12000 || referrals.Link != 1 || referrals.IRL != 1 {
		t.Fatal("wrong split", referrals)
	}
	for _, modification := range []string{
		"net_minor=NULL", "next_refresh_at=computed_at", "referral_kind=NULL",
	} {
		mustExec(t, tx, "SAVEPOINT invalid_projection")
		mustExec(t, tx, "UPDATE donor_metric_projections SET "+modification+" WHERE donor_id=$1", first)
		result, err = dashboard(ctx, q, owner, now)
		if err != nil || result.Referrals != nil {
			t.Fatal("incomplete aggregate exposed", modification, result, err)
		}
		mustExec(t, tx, "ROLLBACK TO SAVEPOINT invalid_projection")
	}
	for _, sameChannel := range []bool{false, true} {
		mustExec(t, tx, "UPDATE donor_metric_projections SET net_minor=$1 WHERE donor_id=$2", int64(math.MaxInt64), first)
		if sameChannel {
			mustExec(t, tx, "UPDATE donor_metric_projections SET referral_kind='LINK' WHERE donor_id=$1", second)
		}
		result, err = dashboard(ctx, q, owner, now)
		if err != nil || result.Referrals != nil {
			t.Fatal("overflowing aggregate exposed", result, err)
		}
	}
}

// A referred donor whose gift is collected but not yet projected holds back
// the referrer's totals; one who only started a checkout does not.
func TestDashboardWaitsForReferredGiftsToBeProjected(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	owner, giver, browser := testID(t), testID(t), testID(t)
	for _, id := range []pgtype.UUID{owner, giver, browser} {
		mustExec(t, tx, "INSERT INTO donors(id,email) VALUES($1,$2)", id, id.String()+"@example.org")
		mustExec(t, tx, "INSERT INTO donor_metric_projections(id,donor_id,computed_at,next_refresh_at) VALUES($1,$2,$3,$3)", testID(t), id, now)
	}
	mustExec(t, tx, "UPDATE donor_metric_projections SET net_minor=0,current_streak=0,next_refresh_at=$2 WHERE donor_id=$1", owner, now.Add(time.Hour))
	expires := now.Add(24 * time.Hour)
	for _, id := range []pgtype.UUID{giver, browser} {
		mustExec(t, tx, "INSERT INTO donor_checkouts(id,donor_id,amount_minor,frequency,referrer_id,referral_kind,referral_expires_at) VALUES($1,$2,3000,'ONE_TIME',$3,'LINK',$4)", id, id, owner, expires)
	}
	mustExec(t, tx, "INSERT INTO donor_gifts(id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at,referrer_id,referral_kind) VALUES($1,$2,$2,$3,3000,'USD','ONE_TIME',$4,$5,'LINK')", testID(t), giver, "pi_"+giver.String(), now, owner)
	q := queries.New(tx)
	if result, err := dashboard(ctx, q, owner, now); err != nil || result.Referrals != nil {
		t.Fatalf("totals shown while a referred gift awaits projection: %+v %v", result.Referrals, err)
	}
	mustExec(t, tx, "UPDATE donor_metric_projections SET referrer_id=$1,referral_kind='LINK',net_minor=3000,next_refresh_at=$3 WHERE donor_id=$2", owner, giver, now.Add(time.Hour))
	result, err := dashboard(ctx, q, owner, now)
	if err != nil || result.Referrals == nil || result.Referrals.Link != 1 || result.Referrals.TotalMinor != 3000 {
		t.Fatalf("an unpaid referred checkout held back the totals: %+v %v", result.Referrals, err)
	}
}

// The web dashboard reads these exact fields.
func TestDashboardJSONContract(t *testing.T) {
	at := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	grace := at.Add(14 * 24 * time.Hour)
	for _, c := range []struct {
		board DonorDashboard
		want  string
	}{
		{DonorDashboard{Mode: "test", State: "preparing"}, `{"mode":"test","state":"preparing"}`},
		{DonorDashboard{Mode: "test", State: "ready", ComputedAt: at}, `{"computed_at":"2027-01-02T03:04:05Z","founding_donor":false,"mode":"test","referrals":null,"state":"ready","streak":null}`},
		{DonorDashboard{Mode: "test", State: "ready", ComputedAt: at, FoundingDonor: true,
			Referrals: &ReferralSummary{Link: 1, IRL: 2, LinkMinor: 300, IRLMinor: 400, TotalMinor: 700, Currency: "USD"},
			Streak:    &StreakSummary{Current: 3, AtRisk: true, GraceEndsAt: &grace}},
			`{"computed_at":"2027-01-02T03:04:05Z","founding_donor":true,"mode":"test","referrals":{"currency":"USD","irl":2,"irl_minor":400,"link":1,"link_minor":300,"total_minor":700},"state":"ready","streak":{"at_risk":true,"current":3,"grace_ends_at":"2027-01-16T03:04:05Z"}}`},
	} {
		got, err := json.Marshal(c.board)
		if err != nil || string(got) != c.want {
			t.Fatalf("got %s %v\nwant %s", got, err, c.want)
		}
	}
}
