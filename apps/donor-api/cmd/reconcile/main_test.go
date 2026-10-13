package main

import (
	"context"
	"os"
	"testing"
	"time"

	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/dbroles/dbrolestest"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/reconcile"
	"impactgraph/donor-api/internal/store/queries"
)

// fakeGateway serves canned charge/refund objects to reconcileWindow, so the
// Stripe side is exercised without any live or SDK call. The real StripeGateway
// is tested against an injected httptest server in internal/payments.
type fakeGateway struct {
	charges, refunds []payments.Object
}

func (f fakeGateway) ListChargesByCreated(context.Context, time.Time, time.Time) ([]payments.Object, error) {
	return f.charges, nil
}
func (f fakeGateway) ListRefundsByCreated(context.Context, time.Time, time.Time) ([]payments.Object, error) {
	return f.refunds, nil
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
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; see .agents/tasks/reconcile-plan.md prerequisite")
	}
	pool := dbrolestest.Open(t, dbroles.ReconcileJob)
	q := queries.New(pool)
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
		gw := fakeGateway{charges: objects(t, `{"id":"ch_missing","amount":3000,"livemode":false,"created":`+itoa(noon)+`}`)}
		report, err := reconcileWindow(context.Background(), win, gw, q)
		if err != nil {
			t.Fatal(err)
		}
		if report.MissingGifts != 1 || !report.HasDiscrepancies() {
			t.Fatalf("want one missing gift, got %+v", report)
		}
	})
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
