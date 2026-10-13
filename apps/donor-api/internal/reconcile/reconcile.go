// Package reconcile diffs the charges and refunds Stripe reports for a day
// against the gifts and refunds the database recorded, and names the
// discrepancies. It is independent of HTTP and Stripe, like
// internal/recognition, so it is unit-testable with plain fixtures: callers
// fetch the Stripe objects and database rows and hand them here.
//
// Scope for R1 is charges and refunds only. Invoices, billing periods and
// subscriptions are a phase-2 concern; the SeamPhase2 note below marks where
// that reconciliation would attach, but none of it is built here.
package reconcile

import (
	"errors"
	"sort"
	"time"
	_ "time/tzdata" // America/New_York resolves inside the stripped container.

	"impactgraph/donor-api/internal/payments"
)

// ErrWindow reports an unusable reconciliation window.
var ErrWindow = errors.New("invalid reconciliation window")

// NewYork is the system's civil-time zone: finance reasons about a day as a
// full New York calendar day, as the Founding cutoff and projection
// boundaries do.
var NewYork = mustLoad("America/New_York")

func mustLoad(name string) *time.Location {
	zone, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return zone
}

// SeamPhase2 marks where invoice / billing-period / subscription
// reconciliation would attach. R1 reconciles charges and refunds only
// (user decision 3); this phase deliberately builds none of it.
const SeamPhase2 = "invoice/billing-period/subscription reconciliation is phase-2 and intentionally unbuilt"

// Window is the UTC half-open interval [Start, End) a run reconciles, the New
// York calendar day it represents, and the grace margin that keeps a
// just-created Stripe object near End from being flagged as missing.
type Window struct {
	Start  time.Time     `json:"start"`
	End    time.Time     `json:"end"`
	MaxLag time.Duration `json:"max_lag"`
	NYDate string        `json:"ny_date"`
}

// YesterdayWindow returns the window for the New York calendar day before
// now, with the given grace margin. The reconciliation runs just after NY
// midnight on the day that just closed.
func YesterdayWindow(now time.Time, maxLag time.Duration) (Window, error) {
	ny := now.In(NewYork)
	midnight := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, NewYork)
	return DayWindow(midnight.AddDate(0, 0, -1), maxLag)
}

// DayWindow returns the window for the New York calendar day that day falls
// in, with the given grace margin. day is interpreted in New York so a
// -date=2027-03-01 flag covers that whole NY day.
func DayWindow(day time.Time, maxLag time.Duration) (Window, error) {
	if maxLag < 0 {
		return Window{}, ErrWindow
	}
	ny := day.In(NewYork)
	start := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, NewYork)
	end := start.AddDate(0, 0, 1)
	return RangeWindow(start, end, maxLag)
}

// RangeWindow returns the window [start, end) with the given grace margin,
// for an explicit -since/-until pair. Bounds are kept in UTC.
func RangeWindow(start, end time.Time, maxLag time.Duration) (Window, error) {
	if start.IsZero() || end.IsZero() || !end.After(start) || maxLag < 0 {
		return Window{}, ErrWindow
	}
	return Window{Start: start.UTC(), End: end.UTC(), MaxLag: maxLag, NYDate: start.In(NewYork).Format("2006-01-02")}, nil
}

// StripeCreatedRange is the inclusive Unix-second range for Stripe's created
// filter. End is inclusive of the window's last second.
func (w Window) StripeCreatedRange() (start, end int64) {
	return w.Start.Unix(), w.End.Add(-time.Second).Unix()
}

// withinGrace reports whether created falls in the last MaxLag of the window,
// where a missing database row is a legitimate in-flight event, not a loss.
func (w Window) withinGrace(created time.Time) bool {
	return w.MaxLag > 0 && !created.Before(w.End.Add(-w.MaxLag))
}

// Gift is the subset of a donor_gifts row the diff needs. StripeObjectID is
// the charge id; StripePaymentIntentID is its fallback match key.
type Gift struct {
	StripeObjectID        string
	StripePaymentIntentID string
	AmountMinor           int64
	Currency              string
	PaidAt                time.Time
}

// Refund is the subset of a donor_refunds row the diff needs.
type Refund struct {
	StripeRefundID string
	AmountMinor    int64
	Status         string
	ObservedAt     time.Time
}

// Report is the aggregate verdict. Counts drive the alarm; the id lists name
// the offending objects for an operator. IDS ONLY — never donor PII or
// payloads, per the Financial consistency rule in docs/architecture.md.
type Report struct {
	Window             Window   `json:"window"`
	MissingGifts       int      `json:"missing_gifts"`
	MissingRefunds     int      `json:"missing_refunds"`
	AmountMismatches   int      `json:"amount_mismatches"`
	StatusMismatches   int      `json:"status_mismatches"`
	DBWithoutStripe    int      `json:"db_without_stripe"`
	MissingGiftIDs     []string `json:"missing_gift_ids"`
	MissingRefundIDs   []string `json:"missing_refund_ids"`
	AmountMismatchIDs  []string `json:"amount_mismatch_ids"`
	StatusMismatchIDs  []string `json:"status_mismatch_ids"`
	DBWithoutStripeIDs []string `json:"db_without_stripe_ids"`
}

// maxIDs bounds each id list so a bad day cannot print an unbounded report.
const maxIDs = 100

// HasDiscrepancies reports whether any class has a non-zero count, which the
// CLI turns into a non-zero exit so an alarm fires.
func (r Report) HasDiscrepancies() bool {
	return r.MissingGifts > 0 || r.MissingRefunds > 0 || r.AmountMismatches > 0 ||
		r.StatusMismatches > 0 || r.DBWithoutStripe > 0
}

// refundStatus maps a Stripe lowercase refund status to the uppercase DB enum.
func refundStatus(stripe string) string {
	switch stripe {
	case "pending":
		return "PENDING"
	case "requires_action":
		return "REQUIRES_ACTION"
	case "succeeded":
		return "SUCCEEDED"
	case "failed":
		return "FAILED"
	case "canceled":
		return "CANCELED"
	default:
		return ""
	}
}

// Diff classifies every discrepancy between the Stripe window and the
// database rows. An unattributed-but-present gift is NOT a discrepancy:
// attribution is a downstream projection concern, not money reconciliation.
func Diff(window Window, stripeCharges, stripeRefunds []payments.Object, dbGifts []Gift, dbRefunds []Refund) Report {
	report := Report{
		Window:             window,
		MissingGiftIDs:     []string{},
		MissingRefundIDs:   []string{},
		AmountMismatchIDs:  []string{},
		StatusMismatchIDs:  []string{},
		DBWithoutStripeIDs: []string{},
	}

	// Index the database rows by every Stripe key they can match on.
	giftByCharge := map[string]Gift{}
	giftByIntent := map[string]Gift{}
	for _, g := range dbGifts {
		if g.StripeObjectID != "" {
			giftByCharge[g.StripeObjectID] = g
		}
		if g.StripePaymentIntentID != "" {
			giftByIntent[g.StripePaymentIntentID] = g
		}
	}
	refundByID := map[string]Refund{}
	for _, r := range dbRefunds {
		if r.StripeRefundID != "" {
			refundByID[r.StripeRefundID] = r
		}
	}

	// Charges: a Stripe charge must have a gift by charge id, else by its
	// payment intent. Missing (outside the grace margin) or amount-mismatched
	// charges are flagged.
	seenGift := map[string]bool{}
	for _, charge := range stripeCharges {
		id := charge.Text("id")
		if id == "" {
			continue
		}
		created, _ := charge.Unix("created")
		gift, ok := giftByCharge[id]
		if !ok {
			gift, ok = giftByIntent[charge.Text("payment_intent")]
		}
		if !ok {
			if window.withinGrace(created) {
				continue
			}
			report.MissingGifts++
			appendID(&report.MissingGiftIDs, id)
			continue
		}
		seenGift[gift.StripeObjectID] = true
		if amount, err := charge.Int("amount"); err == nil && amount != gift.AmountMinor {
			report.AmountMismatches++
			appendID(&report.AmountMismatchIDs, id)
		}
	}

	// Refunds: a Stripe refund must have a donor_refunds row by refund id.
	// Missing (outside grace), amount-mismatched or status-mismatched refunds
	// are flagged.
	seenRefund := map[string]bool{}
	for _, refund := range stripeRefunds {
		id := refund.Text("id")
		if id == "" {
			continue
		}
		created, _ := refund.Unix("created")
		row, ok := refundByID[id]
		if !ok {
			if window.withinGrace(created) {
				continue
			}
			report.MissingRefunds++
			appendID(&report.MissingRefundIDs, id)
			continue
		}
		seenRefund[id] = true
		if amount, err := refund.Int("amount"); err == nil && amount != row.AmountMinor {
			report.AmountMismatches++
			appendID(&report.AmountMismatchIDs, id)
		}
		if want := refundStatus(refund.Text("status")); want != "" && want != row.Status {
			report.StatusMismatches++
			appendID(&report.StatusMismatchIDs, id)
		}
	}

	// DB rows with no Stripe counterpart: a recorded fact Stripe does not
	// confirm. Expected to be empty; a hit is reported, never repaired.
	for _, g := range dbGifts {
		if g.StripeObjectID != "" && !seenGift[g.StripeObjectID] {
			report.DBWithoutStripe++
			appendID(&report.DBWithoutStripeIDs, g.StripeObjectID)
		}
	}
	for _, r := range dbRefunds {
		if r.StripeRefundID != "" && !seenRefund[r.StripeRefundID] {
			report.DBWithoutStripe++
			appendID(&report.DBWithoutStripeIDs, r.StripeRefundID)
		}
	}

	sortIDs(&report)
	return report
}

func appendID(list *[]string, id string) {
	if len(*list) < maxIDs {
		*list = append(*list, id)
	}
}

// sortIDs makes the report deterministic for a given Stripe/DB state so a
// re-run produces the same output.
func sortIDs(r *Report) {
	for _, list := range [][]string{r.MissingGiftIDs, r.MissingRefundIDs, r.AmountMismatchIDs, r.StatusMismatchIDs, r.DBWithoutStripeIDs} {
		sort.Strings(list)
	}
}
