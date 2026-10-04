package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

func TestFinancialOutboxDeduplicationYearBoundaryAndRollback(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	id, referrer := testID(t), testID(t)
	for _, donor := range []pgtype.UUID{id, referrer} {
		if err = q.InsertGuest(ctx, queries.InsertGuestParams{ID: donor, Email: donor.String() + "@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2028, 1, 1, 0, 30, 0, 0, time.UTC) // Still 2027 in the receipt tax-year zone.
	for _, paid := range []time.Time{time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC), time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)} {
		draft, gift := testID(t), testID(t)
		mustExec(t, tx, `INSERT INTO donor_checkouts (id,donor_id,amount_minor,currency,frequency) VALUES ($1,$2,3000,'USD','ONE_TIME')`, draft, id)
		mustExec(t, tx, `INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at) VALUES ($1,$2,$3,$4,3000,'USD','ONE_TIME',$5)`, gift, id, draft, "pi_"+gift.String(), paid)
	}
	draft, period := testID(t), testID(t)
	due := now.Add(-8 * 24 * time.Hour)
	mustExec(t, tx, `INSERT INTO donor_checkouts (id,donor_id,amount_minor,currency,frequency) VALUES ($1,$2,3000,'USD','MONTHLY')`, draft, id)
	mustExec(t, tx, `INSERT INTO donor_billing_periods (id,donor_id,checkout_id,stripe_invoice_id,stripe_subscription_id,due_at,ends_at,status) VALUES ($1,$2,$3,$4,'sub_test',$5,$6,'FAILED')`, period, id, draft, "in_"+period.String(), due, due.Add(30*24*time.Hour))
	if err = q.SaveProjection(ctx, queries.SaveProjectionParams{ID: testID(t), DonorID: id, ReferrerID: referrer, ReferralKind: pgtype.Text{String: "IRL", Valid: true}, NetMinor: pgtype.Int8{Int64: 6000, Valid: true}, ComputedAt: timestamp(now), NextRefreshAt: timestamp(now.Add(time.Hour))}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = QueueFinancialMessages(ctx, q, id, now); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM donor_messages WHERE donor_id=$1 OR donor_id=$2", id, referrer).Scan(&count); err != nil || count != 7 {
		t.Fatalf("wrong outbox cardinality: %d %v", count, err)
	}
	var year int
	if err = tx.QueryRow(ctx, "SELECT (context->>'year')::integer FROM donor_messages WHERE donor_id=$1 AND kind='ANNUAL'", id).Scan(&year); err != nil || year != 2026 {
		t.Fatal("annual summary crossed New York year boundary")
	}
	var channel string
	if err = tx.QueryRow(ctx, "SELECT context->>'kind' FROM donor_messages WHERE donor_id=$1 AND kind='REFERRAL'", referrer).Scan(&channel); err != nil || channel != "IRL" {
		t.Fatal("referral channel lost")
	}
	rows, err := tx.Query(ctx, "SELECT (context->>'day')::integer,available_at FROM donor_messages WHERE donor_id=$1 AND kind='RECOVERY'", id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var day int
		var at time.Time
		if err = rows.Scan(&day, &at); err != nil {
			t.Fatal(err)
		}
		if !at.Equal(due.Add(time.Duration(day) * 24 * time.Hour)) {
			t.Fatal("recovery timing changed")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM donor_messages WHERE donor_id=$1 OR donor_id=$2", id, referrer).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled back financial messages persisted")
	}
}
