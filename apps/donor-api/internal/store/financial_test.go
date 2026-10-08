package store

import (
	"context"
	"testing"
	"time"

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
func mustExec(t *testing.T, tx queries.DBTX, sql string, args ...any) {
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
		id, period, referrer pgtype.UUID
		paid                 time.Time
	}{{firstGift, firstPeriod, referrerID, jan}, {secondGift, secondPeriod, pgtype.UUID{}, feb}} {
		mustExec(t, tx, `INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at,period_id,referrer_id,referral_kind) VALUES ($1,$2,$3,$4,3000,'USD','MONTHLY',$5,$6,$7,CASE WHEN $7::uuid IS NULL THEN NULL ELSE 'IRL' END)`, g.id, donorID, checkout, "in_"+g.period.String(), g.paid, g.period, g.referrer)
	}
	preparing, err := dashboard(ctx, q, referrerID, now)
	if err != nil || preparing.State != "preparing" {
		t.Fatalf("missing projection: %v %v", preparing, err)
	}
	if _, err = projectFacts(ctx, q, referrerID, now); err != nil {
		t.Fatal(err)
	}
	missing, err := dashboard(ctx, q, referrerID, now)
	if err != nil || missing.Referrals != nil {
		t.Fatal("missing referred history shown as zero")
	}
	initial, err := projectFacts(ctx, q, donorID, now)
	if err != nil || !initial.CurrentStreak.Valid || initial.CurrentStreak.Int32 != 2 || initial.BestStreak != 2 || initial.NetMinor.Int64 != 6000 || initial.ReferrerID != referrerID || initial.ReferralKind.String != "IRL" || !initial.FoundingAwardedAt.Valid {
		t.Fatalf("wrong initial projection: %+v %v", initial, err)
	}
	view, err := dashboard(ctx, q, referrerID, now)
	if err != nil {
		t.Fatal(err)
	}
	referrals := view.Referrals
	if referrals == nil || referrals.IRL != 1 || referrals.Link != 0 || referrals.TotalMinor != 6000 {
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
	refunded, err := projectFacts(ctx, q, donorID, now)
	if err != nil || refunded.NetMinor.Int64 != 3000 || refunded.CurrentStreak.Int32 != 0 || refunded.BestStreak != 2 || !refunded.FoundingAwardedAt.Time.Equal(initial.FoundingAwardedAt.Time) {
		t.Fatalf("wrong refunded projection: %+v %v", refunded, err)
	}
	stale, err := dashboard(ctx, q, referrerID, now.Add(24*time.Hour))
	if err != nil || stale.Referrals != nil || stale.Streak != nil {
		t.Fatal("stale values exposed")
	}
	// An unobserved renewal keeps the current count through its grace window.
	due, err := projectFacts(ctx, q, donorID, mar)
	if err != nil || !due.CurrentStreak.Valid || due.CurrentStreak.Int32 != 0 || due.AtRisk || !due.NextRefreshAt.Time.Equal(mar.Add(24*time.Hour)) {
		t.Fatalf("unobserved renewal: %+v %v", due, err)
	}
	// Missing renewal history past grace cannot manufacture a zero streak.
	lapsed := mar.Add(14 * 24 * time.Hour)
	late, err := projectFacts(ctx, q, donorID, lapsed)
	if err != nil || late.CurrentStreak.Valid || !late.NextRefreshAt.Time.Equal(lapsed.Add(5*time.Minute)) {
		t.Fatalf("missing history: %+v %v", late, err)
	}
}
