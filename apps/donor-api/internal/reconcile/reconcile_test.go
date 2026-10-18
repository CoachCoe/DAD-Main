package reconcile

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/payments"
)

// obj builds a payments.Object from a JSON literal for the fixtures.
func obj(t *testing.T, raw string) payments.Object {
	t.Helper()
	o, err := payments.DecodeObject([]byte(raw))
	if err != nil {
		t.Fatalf("bad fixture %q: %v", raw, err)
	}
	defaults := map[string]json.RawMessage{"livemode": json.RawMessage(`false`), "currency": json.RawMessage(`"usd"`)}
	if strings.HasPrefix(o.Text("id"), "ch_") {
		defaults["status"], defaults["paid"], defaults["captured"] = json.RawMessage(
			`"succeeded"`,
		), json.RawMessage(
			`true`,
		), json.RawMessage(
			`true`,
		)
		defaults["amount_captured"] = o["amount"]
	}
	for key, value := range defaults {
		if _, exists := o[key]; !exists {
			o[key] = value
		}
	}
	return o
}

func TestYesterdayWindowIsAFullNewYorkDay(t *testing.T) {
	// 2027-03-02 00:30 New York (EST, UTC-5) -> yesterday is the whole of
	// 2027-03-01 in New York.
	now := time.Date(2027, 3, 2, 5, 30, 0, 0, time.UTC)
	w, err := YesterdayWindow(now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w.NYDate != "2027-03-01" {
		t.Errorf("ny date %q", w.NYDate)
	}
	wantStart := time.Date(2027, 3, 1, 0, 0, 0, 0, NewYork).UTC()
	wantEnd := time.Date(2027, 3, 2, 0, 0, 0, 0, NewYork).UTC()
	if !w.Start.Equal(wantStart) || !w.End.Equal(wantEnd) {
		t.Errorf("window %v..%v, want %v..%v", w.Start, w.End, wantStart, wantEnd)
	}
	gte, lte := w.StripeCreatedRange()
	if gte != wantStart.Unix() || lte != wantEnd.Add(-time.Second).Unix() {
		t.Errorf("created range %d..%d", gte, lte)
	}
}

func TestRangeWindowRejectsEmptyAndNegativeLag(t *testing.T) {
	start := time.Unix(1000, 0)
	if _, err := RangeWindow(start, start, 0); err == nil {
		t.Error("empty window accepted")
	}
	if _, err := RangeWindow(start, start.Add(time.Hour), -time.Minute); err == nil {
		t.Error("negative grace accepted")
	}
}

func TestDiffClassifiesEachDiscrepancy(t *testing.T) {
	// Window: 2027-03-01 full NY day, 30m grace. End is 2027-03-02 00:00 NY.
	w, err := DayWindow(time.Date(2027, 3, 1, 12, 0, 0, 0, NewYork), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	noon := time.Date(2027, 3, 1, 12, 0, 0, 0, NewYork).Unix()
	// 15 minutes before the window closes -> inside the 30m grace margin.
	nearEdge := w.End.Add(-15 * time.Minute).Unix()

	for _, tc := range []struct {
		name          string
		charges       []payments.Object
		refunds       []payments.Object
		gifts         []Gift
		dbRefunds     []Refund
		want          Report
		noDiscrepancy bool
	}{
		{
			name:          "clean day",
			charges:       []payments.Object{obj(t, `{"id":"ch_1","amount":3000,"livemode":false,"created":`+strconv.FormatInt(noon, 10)+`}`)},
			gifts:         []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000, Currency: "USD"}},
			noDiscrepancy: true,
		},
		{
			name:    "missing gift",
			charges: []payments.Object{obj(t, `{"id":"ch_lost","amount":3000,"created":`+strconv.FormatInt(noon, 10)+`}`)},
			want:    Report{MissingGifts: 1},
		},
		{
			name:          "charge matched by payment intent, not charge id",
			charges:       []payments.Object{obj(t, `{"id":"ch_1","payment_intent":"pi_1","amount":3000,"created":`+strconv.FormatInt(noon, 10)+`}`)},
			gifts:         []Gift{{StripeObjectID: "pi_1", StripePaymentIntentID: "pi_1", AmountMinor: 3000, Currency: "USD"}},
			noDiscrepancy: true,
		},
		{
			name:    "settled window includes boundary charges",
			charges: []payments.Object{obj(t, `{"id":"ch_inflight","amount":3000,"created":`+strconv.FormatInt(nearEdge, 10)+`}`)},
			want:    Report{MissingGifts: 1},
		},
		{
			name:    "amount mismatch",
			charges: []payments.Object{obj(t, `{"id":"ch_1","amount":5000,"created":`+strconv.FormatInt(noon, 10)+`}`)},
			gifts:   []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000, Currency: "USD"}},
			want:    Report{AmountMismatches: 1},
		},
		{
			name:    "missing refund",
			refunds: []payments.Object{obj(t, `{"id":"re_lost","amount":1000,"status":"succeeded","created":`+strconv.FormatInt(noon, 10)+`}`)},
			want:    Report{MissingRefunds: 1},
		},
		{
			name:      "refund status mismatch",
			refunds:   []payments.Object{obj(t, `{"id":"re_1","amount":1000,"status":"succeeded","created":`+strconv.FormatInt(noon, 10)+`}`)},
			dbRefunds: []Refund{{StripeRefundID: "re_1", AmountMinor: 1000, Currency: "USD", Status: "PENDING"}},
			want:      Report{StatusMismatches: 1},
		},
		{
			name:  "db gift without stripe",
			gifts: []Gift{{StripeObjectID: "ch_orphan", AmountMinor: 3000, Currency: "USD"}},
			want:  Report{DBWithoutStripe: 1},
		},
		{
			name:      "db refund without stripe",
			dbRefunds: []Refund{{StripeRefundID: "re_orphan", AmountMinor: 1000, Currency: "USD", Status: "SUCCEEDED"}},
			want:      Report{DBWithoutStripe: 1},
		},
		{
			name:    "unattributed present gift is not a discrepancy",
			charges: []payments.Object{obj(t, `{"id":"ch_1","amount":3000,"created":`+strconv.FormatInt(noon, 10)+`}`)},
			// A gift with no referral attribution is still a reconciled money fact.
			gifts:         []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000, Currency: "USD"}},
			noDiscrepancy: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(w, tc.charges, tc.refunds, tc.gifts, tc.dbRefunds)
			if got.HasDiscrepancies() == tc.noDiscrepancy {
				t.Fatalf("HasDiscrepancies=%v, want %v (report %+v)", got.HasDiscrepancies(), !tc.noDiscrepancy, got)
			}
			if tc.noDiscrepancy {
				return
			}
			if got.MissingGifts != tc.want.MissingGifts || got.MissingRefunds != tc.want.MissingRefunds ||
				got.AmountMismatches != tc.want.AmountMismatches || got.StatusMismatches != tc.want.StatusMismatches ||
				got.DBWithoutStripe != tc.want.DBWithoutStripe {
				t.Errorf("counts %+v, want %+v", countsOf(got), countsOf(tc.want))
			}
			if got.CurrencyMismatches != 0 || got.InvalidObjects != 0 {
				t.Fatal("valid fixture reported invalid financial facts", got)
			}
		})
	}
}

// TestReportIsIDsOnly guards the output-hygiene rule: the JSON report carries
// ids and counts only, no donor fields or payloads.
func TestReportIsIDsOnly(t *testing.T) {
	w, _ := RangeWindow(time.Unix(1000, 0), time.Unix(2000, 0), 0)
	report := Diff(w, []payments.Object{obj(t, `{"id":"ch_lost","amount":1,"created":1500}`)}, nil, nil, nil)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"donor", "email", "payment_intent", "amount_minor\":", "payload"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("report leaks %q: %s", forbidden, data)
		}
	}
	malformed := Diff(
		w,
		nil,
		nil,
		[]Gift{{StripeObjectID: "private@example.org", AmountMinor: 1, Currency: "USD"}},
		[]Refund{{StripeRefundID: "private@example.org", AmountMinor: 1, Currency: "USD", Status: "SUCCEEDED"}},
	)
	data, err = json.Marshal(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if malformed.InvalidObjects != 2 || malformed.DBWithoutStripe != 2 || strings.Contains(string(data), "private@example.org") {
		t.Fatalf("malformed stored identifiers were hidden or disclosed: %s", data)
	}
}

func TestClosedWindowDoesNotHideItsFinalHalfHour(t *testing.T) {
	w, err := DayWindow(time.Date(2027, 3, 1, 0, 0, 0, 0, NewYork), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	charge := obj(
		t,
		`{"id":"ch_boundary","payment_intent":"pi_boundary","amount":3000,"amount_captured":3000,"currency":"usd","status":"succeeded","paid":true,"captured":true,"livemode":false,"created":`+strconv.FormatInt(
			w.End.Add(-time.Minute).Unix(),
			10,
		)+`}`,
	)
	if got := Diff(w, []payments.Object{charge}, nil, nil, nil); got.MissingGifts != 1 {
		t.Fatal("closed window hid a missing boundary gift", got)
	}
}

func TestUncollectedChargeDoesNotRequireAGift(t *testing.T) {
	w, _ := RangeWindow(time.Unix(1000, 0), time.Unix(2000, 0), 0)
	charge := obj(
		t,
		`{"id":"ch_failed","amount":3000,"amount_captured":0,"currency":"usd","status":"failed","paid":false,"captured":false,"livemode":false,"created":1500}`,
	)
	if got := Diff(w, []payments.Object{charge}, nil, nil, nil); got.HasDiscrepancies() {
		t.Fatal("failed charge reported as a missing gift", got)
	}
}

func TestCurrencyAndMalformedAmountsCannotPassReconciliation(t *testing.T) {
	w, _ := RangeWindow(time.Unix(1000, 0), time.Unix(2000, 0), 0)
	for _, raw := range []string{
		`{"id":"ch_bad","amount":3000,"amount_captured":3000,"currency":"eur","status":"succeeded","paid":true,"captured":true,"livemode":false,"created":1500}`,
		`{"id":"ch_bad","amount":"invalid","amount_captured":3000,"currency":"usd","status":"succeeded","paid":true,"captured":true,"livemode":false,"created":1500}`,
	} {
		got := Diff(w, []payments.Object{obj(t, raw)}, nil, []Gift{{StripeObjectID: "ch_bad", AmountMinor: 3000, Currency: "USD"}}, nil)
		if !got.HasDiscrepancies() {
			t.Fatal("invalid financial comparison passed", got)
		}
	}
}

func countsOf(r Report) [5]int {
	return [5]int{r.MissingGifts, r.MissingRefunds, r.AmountMismatches, r.StatusMismatches, r.DBWithoutStripe}
}

func TestWindowWaitsForSettlementAndKeepsFractionalBounds(t *testing.T) {
	w, _ := RangeWindow(time.Unix(1000, 500000000), time.Unix(2000, 500000000), 30*time.Minute)
	start, end := w.StripeCreatedRange()
	if start != 1001 || end != 2000 {
		t.Fatal("fractional range included or lost a Stripe timestamp", start, end)
	}
	if w.Settled(w.End.Add(w.MaxLag-time.Nanosecond)) || !w.Settled(w.End.Add(w.MaxLag)) {
		t.Fatal("settlement boundary is not enforced")
	}
	for _, tc := range []struct {
		date  string
		hours time.Duration
	}{{"2027-03-14", 23}, {"2027-11-07", 25}} {
		day, err := time.ParseInLocation(time.DateOnly, tc.date, NewYork)
		if err != nil {
			t.Fatal(err)
		}
		window, err := DayWindow(day, 0)
		if err != nil || window.End.Sub(window.Start) != tc.hours*time.Hour {
			t.Fatal("New York DST day has the wrong bounds", window, err)
		}
	}
}

func TestFailedAttemptBeforeSuccessfulChargeDoesNotInvalidateGift(t *testing.T) {
	w, _ := RangeWindow(time.Unix(1000, 0), time.Unix(2000, 0), 0)
	charges := []payments.Object{
		obj(
			t,
			`{"id":"ch_failed","payment_intent":"pi_1","amount":3000,"amount_captured":0,"status":"failed","paid":false,"captured":false,"created":1400}`,
		),
		obj(t, `{"id":"ch_paid","payment_intent":"pi_1","amount":3000,"created":1500}`),
	}
	got := Diff(w, charges, nil, []Gift{{StripeObjectID: "pi_1", StripePaymentIntentID: "pi_1", AmountMinor: 3000, Currency: "USD"}}, nil)
	if got.HasDiscrepancies() {
		t.Fatal("failed attempt invalidated a confirmed gift", got)
	}
}
