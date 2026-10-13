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
// -max-lag (default 30m) requires the entire window to have settled before it runs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	maxLag := flags.Duration("max-lag", 30*time.Minute, "required processing delay after the window closes")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid reconcile arguments")
	}
	win, err := window(*date, *since, *until, *maxLag, time.Now())
	if err != nil {
		return err
	}
	if !win.Settled(time.Now()) {
		return errors.New("reconciliation window has not settled; wait until its end plus max-lag")
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

	var report reconcile.Report
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		report, err = reconcileWindow(ctx, win, gateway, queries.New(tx))
		return err
	})
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
	charges, refunds := []payments.Object{}, []payments.Object{}
	if gte <= lte {
		var err error
		charges, err = gateway.ListChargesByCreated(ctx, start, end)
		if err != nil {
			return reconcile.Report{}, errors.New("stripe charge listing failed")
		}
		refunds, err = gateway.ListRefundsByCreated(ctx, start, end)
		if err != nil {
			return reconcile.Report{}, errors.New("stripe refund listing failed")
		}
	}

	chargeIDs, paymentIDs, refundIDs := []string{}, []string{}, []string{}
	seenCharges, seenRefunds := map[string]bool{}, map[string]bool{}
	for _, charge := range charges {
		_, collected, err := reconcile.CollectedCharge(charge)
		if err != nil || !collected {
			continue
		}
		id, payment := charge.Text("id"), charge.Text("payment_intent")
		chargeIDs, paymentIDs = append(chargeIDs, id), append(paymentIDs, payment)
		seenCharges[id], seenCharges[payment] = true, true
	}
	for _, refund := range refunds {
		id := refund.Text("id")
		refundIDs = append(refundIDs, id)
		seenRefunds[id] = true
	}
	bound := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	giftRows, err := q.ReconciliationGifts(ctx, queries.ReconciliationGiftsParams{Start: bound(win.Start), EndAt: bound(win.End), StripeObjects: append(chargeIDs, paymentIDs...), PaymentIntents: paymentIDs})
	if err != nil {
		return reconcile.Report{}, errors.New("gift window query failed")
	}
	refundRows, err := q.ReconciliationRefunds(ctx, queries.ReconciliationRefundsParams{Start: bound(win.Start), EndAt: bound(win.End), StripeRefunds: refundIDs})
	if err != nil {
		return reconcile.Report{}, errors.New("refund window query failed")
	}
	if len(giftRows) > 10000 || len(refundRows) > 10000 {
		return reconcile.Report{}, errors.New("reconciliation window exceeds 10000 records; use a smaller range")
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
		if seenCharges[g.StripeObjectID] || (g.StripePaymentIntentID.Valid && seenCharges[g.StripePaymentIntentID.String]) {
			continue
		}
		payment := g.StripePaymentIntentID.String
		if payment == "" && strings.HasPrefix(g.StripeObjectID, "pi_") {
			payment = g.StripeObjectID
		}
		if payment == "" {
			continue
		}
		if err := payments.WaitReconciliationRead(ctx); err != nil {
			return reconcile.Report{}, err
		}
		intent, err := gateway.RetrievePaymentIntent(ctx, payment)
		if err != nil {
			return reconcile.Report{}, errors.New("stripe payment verification failed")
		}
		if intent.Text("id") != payment || !intent.BoolIs("livemode", false) {
			return reconcile.Report{}, errors.New("stripe payment verification returned invalid facts")
		}
		if intent.Text("status") != "succeeded" {
			continue
		}
		charge, err := intent.Object("latest_charge")
		if err != nil || charge.Text("payment_intent") != payment {
			return reconcile.Report{}, errors.New("stripe payment charge verification returned invalid facts")
		}
		charges = append(charges, payments.ReconciliationFacts(charge))
		seenCharges[payment] = true
	}
	dbRefunds := make([]reconcile.Refund, 0, len(refundRows))
	for _, r := range refundRows {
		dbRefunds = append(dbRefunds, reconcile.Refund{
			StripeRefundID: r.StripeRefundID,
			AmountMinor:    r.AmountMinor,
			Currency:       r.Currency,
			Status:         r.Status,
			ObservedAt:     r.ObservedAt.Time,
		})
		if !seenRefunds[r.StripeRefundID] {
			if err := payments.WaitReconciliationRead(ctx); err != nil {
				return reconcile.Report{}, err
			}
			refund, err := gateway.RetrieveRefund(ctx, r.StripeRefundID)
			if err != nil {
				return reconcile.Report{}, errors.New("stripe refund verification failed")
			}
			if refund.Text("id") != r.StripeRefundID {
				return reconcile.Report{}, errors.New("stripe refund verification returned invalid facts")
			}
			refunds = append(refunds, payments.ReconciliationFacts(refund))
		}
	}
	return reconcile.Diff(win, charges, refunds, gifts, dbRefunds), nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
