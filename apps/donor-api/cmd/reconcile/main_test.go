package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/dbroles/dbrolestest"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/reconcile"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/store/queries"
)

// fakeGateway serves canned charge/refund objects to reconcileWindow, so the
// Stripe side is exercised without any live or SDK call. The real StripeGateway
// is tested against an injected httptest server in internal/payments.
type fakeGateway struct {
	charges, refunds          []payments.Object
	paymentsByID, refundsByID map[string]payments.Object
}

func (f fakeGateway) ListChargesByCreated(context.Context, time.Time, time.Time) ([]payments.Object, error) {
	return f.charges, nil
}
func (f fakeGateway) ListRefundsByCreated(context.Context, time.Time, time.Time) ([]payments.Object, error) {
	return f.refunds, nil
}

func (f fakeGateway) RetrievePaymentIntent(_ context.Context, id string) (payments.Object, error) {
	if o, ok := f.paymentsByID[id]; ok {
		return o, nil
	}
	return nil, errors.New("unexpected payment lookup")
}

func (f fakeGateway) RetrieveRefund(_ context.Context, id string) (payments.Object, error) {
	if o, ok := f.refundsByID[id]; ok {
		return o, nil
	}
	return nil, errors.New("unexpected refund lookup")
}

func objects(t *testing.T, raws ...string) []payments.Object {
	t.Helper()
	out := make([]payments.Object, 0, len(raws))
	for _, raw := range raws {
		o, err := payments.DecodeObject([]byte(raw))
		if err != nil {
			t.Fatalf("bad fixture %q: %v", raw, err)
		}
		out = append(out, o)
	}
	return out
}

func TestWindowResolution(t *testing.T) {
	now := time.Date(2027, 3, 2, 5, 30, 0, 0, time.UTC) // 00:30 New York (EST)
	// Default: yesterday in New York.
	def, err := window("", "", "", 30*time.Minute, now)
	if err != nil || def.NYDate != "2027-03-01" {
		t.Fatalf("default window %+v err=%v", def, err)
	}
	// -date fixes the day deterministically.
	byDate, err := window("2027-02-14", "", "", 30*time.Minute, now)
	if err != nil || byDate.NYDate != "2027-02-14" {
		t.Fatalf("dated window %+v err=%v", byDate, err)
	}
	// -since/-until must be given together and parse as RFC3339.
	if _, err = window("", "2027-03-01T00:00:00Z", "", 0, now); err == nil {
		t.Error("lone -since accepted")
	}
	rng, err := window("", "2027-03-01T00:00:00Z", "2027-03-02T00:00:00Z", 0, now)
	if err != nil || !rng.Start.Equal(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("range window %+v err=%v", rng, err)
	}
	if _, err = window("not-a-date", "", "", 0, now); err == nil {
		t.Error("invalid -date accepted")
	}
}

func TestReconcileWindowAgainstSyntheticDB(t *testing.T) {
	_, reader := reconciliationDatabase(t)
	q := queries.New(reader)
	win, err := reconcile.DayWindow(time.Date(2027, 3, 1, 12, 0, 0, 0, reconcile.NewYork), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	noon := time.Date(2027, 3, 1, 12, 0, 0, 0, reconcile.NewYork).Unix()

	t.Run("clean day exits 0", func(t *testing.T) {
		// No seeded gifts in the window and no Stripe objects: a clean day.
		report, err := reconcileWindow(context.Background(), win, fakeGateway{}, q)
		if err != nil {
			t.Fatal(err)
		}
		if report.HasDiscrepancies() {
			t.Fatalf("clean day flagged discrepancies: %+v", report)
		}
	})

	t.Run("missing webhook is one missing gift", func(t *testing.T) {
		gw := fakeGateway{charges: objects(t, `{"id":"ch_missing","amount":3000,"amount_captured":3000,"currency":"usd","status":"succeeded","paid":true,"captured":true,"livemode":false,"created":`+strconv.FormatInt(noon, 10)+`}`)}
		report, err := reconcileWindow(context.Background(), win, gw, q)
		if err != nil {
			t.Fatal(err)
		}
		if report.MissingGifts != 1 || !report.HasDiscrepancies() {
			t.Fatalf("want one missing gift, got %+v", report)
		}
	})
}

func TestReconciliationMatchesAcrossCreationAndObservationDays(t *testing.T) {
	ctx := context.Background()
	owner, reader := reconciliationDatabase(t)
	id := func() string {
		uuid, err := store.NewID()
		if err != nil {
			t.Fatal(err)
		}
		return uuid.String()
	}
	donor, checkout, giftA, giftB, giftC, refundA, refundB, refundC := id(), id(), id(), id(), id(), id(), id(), id()
	piA, piB, reA, reB, reC := "pi_"+giftA, "pi_"+giftB, "re_"+refundA, "re_"+refundB, "re_"+refundC
	win, err := reconcile.DayWindow(time.Date(2035, 3, 1, 0, 0, 0, 0, reconcile.NewYork), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO donors(id,email) VALUES($1,$2)", donor, donor+"@reconciliation.invalid")
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_refunds WHERE gift_id IN (SELECT id FROM donor_gifts WHERE donor_id=$1)",
			"DELETE FROM donor_gifts WHERE donor_id=$1", "DELETE FROM donor_checkouts WHERE donor_id=$1", "DELETE FROM donors WHERE id=$1",
		} {
			if _, err := owner.Exec(context.Background(), sql, donor); err != nil {
				t.Error(err)
			}
		}
	})
	exec("INSERT INTO donor_checkouts(id,donor_id,amount_minor,frequency) VALUES($1,$2,3000,'MONTHLY')", checkout, donor)
	for _, gift := range []struct {
		id, pi string
		paid   time.Time
	}{{giftA, piA, win.End.Add(time.Hour)}, {giftB, piB, win.Start.Add(time.Hour)}, {giftC, "pi_" + giftC, win.Start.Add(-48 * time.Hour)}} {
		exec("INSERT INTO donor_gifts(id,donor_id,checkout_id,stripe_object_id,stripe_payment_intent_id,amount_minor,currency,frequency,paid_at) VALUES($1,$2,$3,$4,$5,3000,'USD','MONTHLY',$6)", gift.id, donor, checkout, "in_"+gift.id, gift.pi, gift.paid)
	}
	for _, refund := range []struct {
		id, gift, stripe string
		observed         time.Time
	}{{refundA, giftB, reA, win.End.Add(2 * time.Hour)}, {refundB, giftC, reB, win.Start.Add(2 * time.Hour)}} {
		exec("INSERT INTO donor_refunds(id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) VALUES($1,$2,$3,1000,'USD','SUCCEEDED',$4)", refund.id, refund.gift, refund.stripe, refund.observed)
	}
	exec("INSERT INTO donor_refunds(id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) VALUES($1,$2,$3,1000,'USD','PENDING',$4)", refundC, giftC, reC, win.Start.Add(-24*time.Hour))
	charge := func(pi string, created time.Time) payments.Object {
		return objects(t, fmt.Sprintf(`{"id":"ch_%s","payment_intent":%q,"amount":3000,"amount_captured":3000,"currency":"usd","status":"succeeded","paid":true,"captured":true,"livemode":false,"created":%d}`, pi, pi, created.Unix()))[0]
	}
	refund := func(id string, created time.Time) payments.Object {
		return objects(t, fmt.Sprintf(`{"id":%q,"amount":1000,"currency":"usd","status":"succeeded","livemode":false,"created":%d}`, id, created.Unix()))[0]
	}
	oldCharge := charge(piB, win.Start.Add(-time.Hour))
	intent := objects(t, fmt.Sprintf(`{"id":%q,"livemode":false,"status":"succeeded"}`, piB))[0]
	intent["latest_charge"], err = json.Marshal(oldCharge)
	if err != nil {
		t.Fatal(err)
	}
	gateway := fakeGateway{
		charges:      []payments.Object{charge(piA, win.Start.Add(time.Hour))},
		refunds:      []payments.Object{refund(reA, win.Start.Add(2*time.Hour))},
		paymentsByID: map[string]payments.Object{piB: intent},
		refundsByID:  map[string]payments.Object{reB: refund(reB, win.Start.Add(-time.Hour)), reC: refund(reC, win.Start.Add(-24*time.Hour))},
	}
	gateway.refundsByID[reC]["status"] = []byte(`"pending"`)
	q := queries.New(reader)
	got, err := reconcileWindow(ctx, win, gateway, q)
	if err != nil || got.HasDiscrepancies() {
		t.Fatal("processing delay or earlier creation caused a false discrepancy", got, err)
	}
	gateway.refundsByID[reC]["status"] = []byte(`"succeeded"`)
	got, err = reconcileWindow(ctx, win, gateway, q)
	if err != nil || got.StatusMismatches != 1 || got.StatusMismatchIDs[0] != reC {
		t.Fatal("missed terminal update of an older refund was not detected", got, err)
	}
	gateway.refundsByID[reC]["status"] = []byte(`"pending"`)
	exec("DELETE FROM donor_refunds WHERE id=$1", refundA)
	got, err = reconcileWindow(ctx, win, gateway, q)
	if err != nil || got.MissingRefunds != 1 || got.MissingRefundIDs[0] != reA {
		t.Fatal("missing delayed refund was not detected", got, err)
	}
	delete(gateway.paymentsByID, piB)
	if _, err := reconcileWindow(ctx, win, gateway, q); err == nil {
		t.Fatal("provider lookup failure passed reconciliation")
	}
}

// Whole-table reconciliation needs a database isolated from concurrent test packages.
func reconciliationDatabase(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	_ = dbrolestest.URL(t, dbroles.ReconcileJob)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	id, err := store.NewID()
	if err != nil {
		t.Fatal(err)
	}
	name := "donor_go_reconcile_" + strings.ReplaceAll(id.String(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	config = config.Copy()
	config.ConnConfig.Database = name
	owner, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	if err := migrations.Apply(ctx, owner, false); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["role"] = dbroles.ReconcileJob.Name
	config.MaxConns = dbroles.ReconcileJob.Pool
	reader, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	return owner, reader
}

func TestRunRejectsAnUnsettledWindowBeforeAccessingProviders(t *testing.T) {
	err := run(context.Background(), []string{"-date", "9999-01-01"}, func(string) string { return "" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "has not settled") {
		t.Fatal("unsettled window was accepted", err)
	}
}
