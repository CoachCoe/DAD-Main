// Command reconcile is the daily Stripe reconciliation job. It lists the
// charges and refunds Stripe created in a past day and diffs them against the
// gifts and refunds the database recorded, prints an aggregate JSON report to
// stdout, and exits non-zero when any discrepancy is found so a CloudWatch
// alarm fires. It is strictly read-only against both Stripe and the database:
// it never repairs, deletes or writes any financial fact. Remediation stays
// manual — re-send the missing event in Stripe, then
// `worker --kind stripe --retry-event <evt>`.
//
// By default it reconciles yesterday as a full calendar day in
// America/New_York (intended to run ~00:30 NY once the day has closed). A
// -date, or an explicit -since/-until pair, makes a re-run deterministic. A
// -max-lag grace margin (default 30m) keeps a just-created Stripe object near
// the window edge from being flagged as a lost event.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/reconcile"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/store/queries"
)

// window resolves the flags into the reconciliation window. -since/-until win
// when both are set; otherwise -date (or, empty, yesterday in New York).
func window(date, since, until string, maxLag time.Duration, now time.Time) (reconcile.Window, error) {
	switch {
	case since != "" || until != "":
		if since == "" || until == "" {
			return reconcile.Window{}, errors.New("-since and -until must be given together")
		}
		start, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return reconcile.Window{}, errors.New("invalid -since; use RFC3339")
		}
		end, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return reconcile.Window{}, errors.New("invalid -until; use RFC3339")
		}
		return reconcile.RangeWindow(start, end, maxLag)
	case date != "":
		day, err := time.ParseInLocation("2006-01-02", date, reconcile.NewYork)
		if err != nil {
			return reconcile.Window{}, errors.New("invalid -date; use YYYY-MM-DD")
		}
		return reconcile.DayWindow(day, maxLag)
	default:
		return reconcile.YesterdayWindow(now, maxLag)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	date := flags.String("date", "", "reconcile this New York calendar day (YYYY-MM-DD); default yesterday in New York")
	since := flags.String("since", "", "window start (RFC3339); use with -until for an explicit range")
	until := flags.String("until", "", "window end, exclusive (RFC3339); use with -since")
	maxLag := flags.Duration("max-lag", 30*time.Minute, "grace margin: a Stripe object created within this of the window edge is not flagged missing")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid reconcile arguments")
	}
	win, err := window(*date, *since, *until, *maxLag, time.Now())
	if err != nil {
		return err
	}

	dsn := dbroles.ReconcileJob.URL(getenv)
	if dsn == "" {
		return errors.New(dbroles.ReconcileJob.Variable() + " is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := store.Open(ctx, dsn, dbroles.ReconcileJob.Pool)
	if err != nil {
		return errors.New("reconcile database unavailable")
	}
	defer pool.Close()
	if err = migrations.Verify(ctx, pool); err != nil {
		return errors.New("reconcile schema is not current")
	}
	if err = dbroles.ReconcileJob.Check(ctx, pool, getenv(dbroles.AllowOwnerVariable) == "true"); err != nil {
		return err
	}

	gateway, err := payments.NewStripeGateway(getenv("STRIPE_SECRET_KEY"))
	if err != nil {
		return errors.New("stripe gateway unavailable")
	}

	report, err := reconcileWindow(ctx, win, gateway, queries.New(pool))
	if err != nil {
		return err
	}
	if err = json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	if report.HasDiscrepancies() {
		return errors.New("stripe reconciliation found discrepancies")
	}
	return nil
}

// reconcileWindow lists the window's Stripe charges and refunds and the
// matching database rows, then diffs them. Both sides are read-only.
func reconcileWindow(ctx context.Context, win reconcile.Window, gateway payments.ReconcileGateway, q *queries.Queries) (reconcile.Report, error) {
	gte, lte := win.StripeCreatedRange()
	start := time.Unix(gte, 0).UTC()
	end := time.Unix(lte, 0).UTC()
	charges, err := gateway.ListChargesByCreated(ctx, start, end)
	if err != nil {
		return reconcile.Report{}, errors.New("stripe charge listing failed")
	}
	refunds, err := gateway.ListRefundsByCreated(ctx, start, end)
	if err != nil {
		return reconcile.Report{}, errors.New("stripe refund listing failed")
	}

	bound := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	giftRows, err := q.GiftsPaidBetween(ctx, queries.GiftsPaidBetweenParams{Start: bound(win.Start), EndAt: bound(win.End)})
	if err != nil {
		return reconcile.Report{}, errors.New("gift window query failed")
	}
	refundRows, err := q.RefundsObservedBetween(ctx, queries.RefundsObservedBetweenParams{Start: bound(win.Start), EndAt: bound(win.End)})
	if err != nil {
		return reconcile.Report{}, errors.New("refund window query failed")
	}

	gifts := make([]reconcile.Gift, 0, len(giftRows))
	for _, g := range giftRows {
		gifts = append(gifts, reconcile.Gift{
			StripeObjectID:        g.StripeObjectID,
			StripePaymentIntentID: g.StripePaymentIntentID.String,
			AmountMinor:           g.AmountMinor,
			Currency:              g.Currency,
			PaidAt:                g.PaidAt.Time,
		})
	}
	dbRefunds := make([]reconcile.Refund, 0, len(refundRows))
	for _, r := range refundRows {
		dbRefunds = append(dbRefunds, reconcile.Refund{
			StripeRefundID: r.StripeRefundID,
			AmountMinor:    r.AmountMinor,
			Status:         r.Status,
			ObservedAt:     r.ObservedAt.Time,
		})
	}
	return reconcile.Diff(win, charges, refunds, gifts, dbRefunds), nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
