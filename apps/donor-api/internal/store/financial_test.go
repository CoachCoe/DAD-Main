package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

func testID(t *testing.T) pgtype.UUID {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustExec(t *testing.T, tx pgx.Tx, sql string, args ...interface{}) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialHistoryProjectionAndDashboard(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	donorID, referrerID := testID(t), testID(t)
	for _, id := range []pgtype.UUID{donorID, referrerID} {
		if err = q.InsertGuest(ctx, queries.InsertGuestParams{ID: id, Email: id.String() + "@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	jan := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	feb := jan.AddDate(0, 1, 0)
	mar := feb.AddDate(0, 1, 0)
	now := feb.Add(4 * 24 * time.Hour)
	checkout, firstPeriod, secondPeriod, firstGift, secondGift := testID(t), testID(t), testID(t), testID(t), testID(t)
	mustExec(t, tx, `INSERT INTO donor_checkouts (id,donor_id,amount_minor,currency,frequency,referrer_id,referral_kind,referral_expires_at) VALUES ($1,$2,3000,'USD','MONTHLY',$3,'IRL',$4)`, checkout, donorID, referrerID, jan.Add(24*time.Hour))
	for _, p := range []struct {
		id         pgtype.UUID
		start, end time.Time
	}{{firstPeriod, jan, feb}, {secondPeriod, feb, mar}} {
		mustExec(t, tx, `INSERT INTO donor_billing_periods (id,donor_id,checkout_id,stripe_invoice_id,stripe_subscription_id,due_at,ends_at,status) VALUES ($1,$2,$3,$4,'sub_test',$5,$6,'PAID')`, p.id, donorID, checkout, "in_"+p.id.String(), p.start, p.end)
	}
	for _, g := range []struct {
		id, period pgtype.UUID
		paid       time.Time
	}{{firstGift, firstPeriod, jan}, {secondGift, secondPeriod, feb}} {
		mustExec(t, tx, `INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at,period_id) VALUES ($1,$2,$3,$4,3000,'USD','MONTHLY',$5,$6)`, g.id, donorID, checkout, "in_"+g.period.String(), g.paid, g.period)
	}
	preparing, err := Dashboard(ctx, q, referrerID, now)
	if err != nil || preparing["state"] != "preparing" {
		t.Fatalf("missing projection: %v %v", preparing, err)
	}
	if _, err = RefreshProjectionFactsInTx(ctx, tx, referrerID, now); err != nil {
		t.Fatal(err)
	}
	missing, err := Dashboard(ctx, q, referrerID, now)
	if err != nil || missing["referrals"] != nil {
		t.Fatal("missing referred history shown as zero")
	}
	initial, err := RefreshProjectionFactsInTx(ctx, tx, donorID, now)
	if err != nil || !initial.CurrentStreak.Valid || initial.CurrentStreak.Int32 != 2 || initial.BestStreak != 2 || initial.NetMinor.Int64 != 6000 || initial.ReferrerID != referrerID || initial.ReferralKind.String != "IRL" || !initial.FoundingAwardedAt.Valid {
		t.Fatalf("wrong initial projection: %+v %v", initial, err)
	}
	dashboard, err := Dashboard(ctx, q, referrerID, now)
	if err != nil {
		t.Fatal(err)
	}
	referrals := dashboard["referrals"].(map[string]interface{})
	if referrals["irl"] != int64(1) || referrals["link"] != int64(0) || referrals["total_minor"] != int64(6000) {
		t.Fatalf("wrong attribution: %v", referrals)
	}
	// A full refund breaks the current streak; the attained best and badge survive.
	refund, event := testID(t), testID(t)
	observed := feb.Add(24 * time.Hour)
	mustExec(t, tx, `INSERT INTO donor_refunds (id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) VALUES ($1,$2,$3,3000,'USD','SUCCEEDED',$4)`, refund, secondGift, "re_"+refund.String(), observed)
	mustExec(t, tx, `INSERT INTO donor_stripe_events (id,stripe_event_id,event_type,occurred_at,data) VALUES ($1,$2,'refund.updated',$3,'{}')`, event, "evt_"+event.String(), observed)
	mustExec(t, tx, `INSERT INTO donor_refund_observations (id,refund_id,source_event_id,status,observed_at) VALUES ($1,$2,$3,'SUCCEEDED',$4)`, testID(t), refund, event, observed)
	// Rebuild from facts, not the existing best, proving historical refund replay.
	mustExec(t, tx, `UPDATE donor_metric_projections SET best_streak=0 WHERE donor_id=$1`, donorID)
	refunded, err := RefreshProjectionFactsInTx(ctx, tx, donorID, now)
	if err != nil || refunded.NetMinor.Int64 != 3000 || refunded.CurrentStreak.Int32 != 0 || refunded.BestStreak != 2 || !refunded.FoundingAwardedAt.Time.Equal(initial.FoundingAwardedAt.Time) {
		t.Fatalf("wrong refunded projection: %+v %v", refunded, err)
	}
	stale, err := Dashboard(ctx, q, referrerID, now.Add(24*time.Hour))
	if err != nil || stale["referrals"] != nil || stale["streak"] != nil {
		t.Fatal("stale values exposed")
	}
	// Missing renewal history cannot manufacture a zero streak.
	late, err := RefreshProjectionFactsInTx(ctx, tx, donorID, mar)
	if err != nil || late.CurrentStreak.Valid || !late.NextRefreshAt.Time.Equal(mar.Add(5*time.Minute)) {
		t.Fatalf("missing history: %+v %v", late, err)
	}
}
