package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/store/queries"
)

func TestReceiptsScopeNewYorkYearAndLaterRefunds(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	owner, other := testID(t), testID(t)
	for _, id := range []string{owner.String(), other.String()} {
		mustExec(t, tx, "INSERT INTO donors(id,email) VALUES($1,$2)", id, id+"@example.org")
	}
	checkout := testID(t)
	mustExec(t, tx, "INSERT INTO donor_checkouts(id,donor_id,amount_minor,currency,frequency) VALUES($1,$2,3000,'USD','ONE_TIME')", checkout, owner)
	early, late, future := testID(t), testID(t), testID(t)
	boundary := time.Date(2027, 1, 1, 5, 0, 0, 0, time.UTC)
	for i, id := range []string{early.String(), late.String(), future.String()} {
		at := boundary.Add(time.Duration(i-1) * time.Second)
		if i == 2 {
			at = boundary.AddDate(2, 0, 0)
		}
		mustExec(t, tx, "INSERT INTO donor_gifts(id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at) VALUES($1,$2,$3,$4,3000,'USD','ONE_TIME',$5)", id, owner, checkout, "pi_"+id, at)
	}
	now := boundary.AddDate(1, 0, 0)
	refund := testID(t)
	mustExec(t, tx, "INSERT INTO donor_refunds(id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) VALUES($1,$2,$3,500,'USD','SUCCEEDED',$4)", refund, early, "re_"+refund.String(), now)
	summary, err := ReadAnnualSummary(ctx, q, owner, 2026, now)
	if err != nil || len(summary.Gifts) != 1 || summary.Gifts[0].ID != early.String() || summary.Amount != 3000 || summary.Refunded != 500 || summary.Net != 2500 {
		t.Fatal("annual boundary/refund mismatch", summary, err)
	}
	if summary.TaxReceipt || !strings.Contains(RenderSummary(summary), "NOT A TAX RECEIPT") {
		t.Fatal("sandbox represented as legal receipt")
	}
	document, err := ReadReceipt(ctx, q, owner, early, now)
	if err != nil || document.Gift.Net != 2500 || !strings.Contains(RenderReceipt(document), "$25.00 USD") {
		t.Fatal(document, err)
	}
	if _, err = ReadReceipt(ctx, q, other, early, now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("other donor's receipt exposed", err)
	}
	if _, err = ReadReceipt(ctx, q, owner, future, now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("future gift exposed", err)
	}
	if _, err = ReadAnnualSummary(ctx, q, owner, 2025, now); !errors.Is(err, ErrReceipt) {
		t.Fatal("invalid year accepted")
	}
	mustExec(t, tx, "UPDATE donor_refunds SET amount_minor=3001 WHERE id=$1", refund)
	if _, err = ReadReceipt(ctx, q, owner, early, now); !errors.Is(err, ErrReceipt) {
		t.Fatal("over-refunded gift accepted", err)
	}
	if money(123456789) != "$1,234,567.89" {
		t.Fatal("money formatting changed")
	}
}
