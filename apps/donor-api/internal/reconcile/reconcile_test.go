package reconcile

import (
	"encoding/json"
	"testing"
	"time"

	"impactgraph/donor-api/internal/payments"
)

// obj builds a payments.Object from a JSON literal for the fixtures.
func obj(t *testing.T, raw string) payments.Object {
	t.Helper()
	o, err := payments.DecodeObject([]byte(raw))
	if err != nil {
		t.Fatalf("bad fixture %q: %v", raw, err)
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
			charges:       []payments.Object{obj(t, `{"id":"ch_1","amount":3000,"livemode":false,"created":`+itoa(noon)+`}`)},
			gifts:         []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000, Currency: "USD"}},
			noDiscrepancy: true,
		},
		{
			name:    "missing gift",
			charges: []payments.Object{obj(t, `{"id":"ch_lost","amount":3000,"created":`+itoa(noon)+`}`)},
			want:    Report{MissingGifts: 1},
		},
		{
			name:    "charge matched by payment intent, not charge id",
			charges: []payments.Object{obj(t, `{"id":"ch_1","payment_intent":"pi_1","amount":3000,"created":`+itoa(noon)+`}`)},
			gifts:   []Gift{{StripeObjectID: "", StripePaymentIntentID: "pi_1", AmountMinor: 3000}},
			// No charge id to flag as db-without-stripe; the gift matches.
			noDiscrepancy: true,
		},
		{
			name:    "grace margin suppresses a boundary charge",
			charges: []payments.Object{obj(t, `{"id":"ch_inflight","amount":3000,"created":`+itoa(nearEdge)+`}`)},
			// No gift row yet, but created is inside the last 30 minutes.
			noDiscrepancy: true,
		},
		{
			name:    "amount mismatch",
			charges: []payments.Object{obj(t, `{"id":"ch_1","amount":5000,"created":`+itoa(noon)+`}`)},
			gifts:   []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000}},
			want:    Report{AmountMismatches: 1},
		},
		{
			name:    "missing refund",
			refunds: []payments.Object{obj(t, `{"id":"re_lost","amount":1000,"status":"succeeded","created":`+itoa(noon)+`}`)},
			want:    Report{MissingRefunds: 1},
		},
		{
			name:      "refund status mismatch",
			refunds:   []payments.Object{obj(t, `{"id":"re_1","amount":1000,"status":"succeeded","created":`+itoa(noon)+`}`)},
			dbRefunds: []Refund{{StripeRefundID: "re_1", AmountMinor: 1000, Status: "PENDING"}},
			want:      Report{StatusMismatches: 1},
		},
		{
			name:  "db gift without stripe",
			gifts: []Gift{{StripeObjectID: "ch_orphan", AmountMinor: 3000}},
			want:  Report{DBWithoutStripe: 1},
		},
		{
			name:      "db refund without stripe",
			dbRefunds: []Refund{{StripeRefundID: "re_orphan", AmountMinor: 1000, Status: "SUCCEEDED"}},
			want:      Report{DBWithoutStripe: 1},
		},
		{
			name:    "unattributed present gift is not a discrepancy",
			charges: []payments.Object{obj(t, `{"id":"ch_1","amount":3000,"created":`+itoa(noon)+`}`)},
			// A gift with no referral attribution is still a reconciled money fact.
			gifts:         []Gift{{StripeObjectID: "ch_1", AmountMinor: 3000}},
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
		if containsField(string(data), forbidden) {
			t.Errorf("report leaks %q: %s", forbidden, data)
		}
	}
}

func countsOf(r Report) [5]int {
	return [5]int{r.MissingGifts, r.MissingRefunds, r.AmountMismatches, r.StatusMismatches, r.DBWithoutStripe}
}

func containsField(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
