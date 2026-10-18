package recognition

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

type scenario struct {
	Name              string          `json:"name"`
	Operation         string          `json:"operation"`
	AsOf              time.Time       `json:"as_of"`
	Gifts             []Gift          `json:"gifts"`
	Refunds           []Refund        `json:"refunds"`
	Periods           []Period        `json:"periods"`
	DonorID           string          `json:"donor_id"`
	PreviousBest      int64           `json:"previous_best"`
	PreviouslyAwarded bool            `json:"previously_awarded"`
	Stops             []time.Time     `json:"stops"`
	Pauses            []Pause         `json:"pauses"`
	Expected          json.RawMessage `json:"expected"`
	Error             string          `json:"error"`
}

func TestSharedFinancialScenarios(t *testing.T) {
	data, err := os.ReadFile("../../testdata/recognition.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []scenario
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("missing financial scenarios")
	}
	for _, c := range cases {
		// Referral totals are aggregated in SQL; the store package runs those scenarios.
		if c.Operation == "referrals" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			var result any
			var err error
			switch c.Operation {
			case "net":
				result, err = NetGifts(c.Gifts, c.Refunds, c.AsOf)
			case "founding":
				result, err = founding(c.Gifts, c.DonorID, c.AsOf, c.PreviouslyAwarded)
			case "streak":
				result, err = GivingStreak(c.Gifts, c.Refunds, c.Periods, c.DonorID, c.AsOf, c.PreviousBest, c.Stops, c.Pauses)
			default:
				t.Fatalf("unknown operation %s", c.Operation)
			}
			if c.Error != "" {
				want := ErrInvalid
				if c.Error == "incomplete" {
					want = ErrIncomplete
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err = json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(c.Expected, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s, want %s", encoded, c.Expected)
			}
		})
	}
}

// founding is the badge the projection shows: permanent once awarded, and
// otherwise earned by a qualifying first gift.
func founding(gifts []Gift, donorID string, asOf time.Time, awarded bool) (bool, error) {
	if asOf.IsZero() {
		return false, ErrInvalid
	}
	if awarded {
		return true, nil
	}
	_, qualified, err := FoundingQualifiedAt(gifts, donorID, asOf)
	return qualified, err
}

func TestEquivalentTimezoneFactsDeduplicateWithoutChangingInput(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	first := Gift{ID: "a", DonorID: "ben", Amount: 3000, PaidAt: now, Frequency: "ONE_TIME", Currency: "USD"}
	second := first
	second.PaidAt = now.In(time.FixedZone("offset", -18000))
	input := []Gift{second, first}
	original := append([]Gift(nil), input...)
	net, err := NetGifts(input, nil, now)
	if err != nil || net["a"] != 3000 {
		t.Fatalf("net=%v err=%v", net, err)
	}
	if _, err = Relationships(input, now); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("calculation modified caller facts")
	}
}

func TestFirstPaymentTimestampTiesUseStableObjectID(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	a := Gift{
		ID:        "pi_a",
		DonorID:   "ben",
		Amount:    3000,
		PaidAt:    now,
		Frequency: "ONE_TIME",
		Currency:  "USD",
		Candidate: &Candidate{ReferrerID: "alice", Kind: "IRL", ExpiresAt: now.Add(time.Hour)},
	}
	z := Gift{
		ID:        "pi_z",
		DonorID:   "ben",
		Amount:    3000,
		PaidAt:    now,
		Frequency: "ONE_TIME",
		Currency:  "USD",
		Candidate: &Candidate{ReferrerID: "cara", Kind: "LINK", ExpiresAt: now.Add(time.Hour)},
	}
	for _, input := range [][]Gift{{a, z}, {z, a}} {
		got, err := Relationships(input, now)
		if err != nil {
			t.Fatal(err)
		}
		r := got["ben"]
		if r.FirstGiftID != "pi_a" || r.ReferrerID == nil || *r.ReferrerID != "alice" || r.Kind == nil || *r.Kind != "IRL" {
			t.Fatalf("timestamp tie depends on input order: %+v", r)
		}
	}
}

func TestFoundingQualifiedAtUsesEarliestQualifyingGift(t *testing.T) {
	early := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	gifts := []Gift{
		{ID: "late", DonorID: "ben", Amount: 100, PaidAt: FoundingCutoff, Frequency: "ONE_TIME", Currency: "USD"},
		{ID: "second", DonorID: "ben", Amount: 100, PaidAt: early.Add(time.Hour), Frequency: "ONE_TIME", Currency: "USD"},
		{ID: "first", DonorID: "ben", Amount: 100, PaidAt: early, Frequency: "ONE_TIME", Currency: "USD"},
		{ID: "other", DonorID: "cara", Amount: 100, PaidAt: early.Add(-time.Hour), Frequency: "ONE_TIME", Currency: "USD"},
	}
	at, ok, err := FoundingQualifiedAt(gifts, "ben", FoundingCutoff.Add(time.Hour))
	if err != nil || !ok || !at.Equal(early) {
		t.Fatalf("qualified=%v at=%s err=%v", ok, at, err)
	}
	if _, ok, err = FoundingQualifiedAt(gifts, "ben", early.Add(-time.Second)); err != nil || ok {
		t.Fatal("future gift qualified", err)
	}
}

func TestPauseFreezeCapIsHalfOpen(t *testing.T) {
	due := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	gifts := []Gift{{ID: "g1", DonorID: "ben", Amount: 3000, PaidAt: due, Frequency: "MONTHLY", PeriodID: "jan", Currency: "USD"}}
	periods := []Period{{ID: "jan", DonorID: "ben", DueAt: due, EndsAt: ends, Status: "EXPECTED"}}
	pauses := []Pause{{Start: ends, End: ends.Add(365 * 24 * time.Hour)}}

	within := PauseFreezeEnd(ends).Add(-time.Nanosecond)
	got, err := GivingStreak(gifts, nil, periods, "ben", within, 0, nil, pauses)
	if err != nil {
		t.Fatalf("within cap: unexpected error %v", err)
	}
	if got.Current != 1 {
		t.Fatalf("within cap: current = %d, want 1 (streak frozen)", got.Current)
	}

	at := PauseFreezeEnd(ends)
	if _, err = GivingStreak(gifts, nil, periods, "ben", at, 0, nil, pauses); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("at cap: err = %v, want ErrIncomplete (freeze expired)", err)
	}
}

func TestMilestonesAreThreeAndSixMonthsThenEveryYear(t *testing.T) {
	var got []int64
	for months := int64(0); months <= 48; months++ {
		if Milestone(months) {
			got = append(got, months)
		}
	}
	if want := []int64{3, 6, 12, 24, 36, 48}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got milestones %v, want %v", got, want)
	}
}

func TestPauseKeepsStreakThroughResumption(t *testing.T) {
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	paused := start.AddDate(0, 1, 0)
	for months := 1; months <= 3; months++ {
		resume := paused.AddDate(0, months, 0)
		periods := []Period{
			{ID: "first", DonorID: "ben", DueAt: start, EndsAt: paused, Status: "EXPECTED"},
			{ID: "resumed", DonorID: "ben", DueAt: resume, EndsAt: resume.AddDate(0, 1, 0), Status: "EXPECTED"},
		}
		gifts := []Gift{
			{ID: "first", DonorID: "ben", Amount: 3000, PaidAt: start, Frequency: "MONTHLY", PeriodID: "first", Currency: "USD"},
			{
				ID:        "resumed",
				DonorID:   "ben",
				Amount:    3000,
				PaidAt:    resume.Add(time.Hour),
				Frequency: "MONTHLY",
				PeriodID:  "resumed",
				Currency:  "USD",
			},
		}
		pauses := []Pause{{Start: paused, End: resume}}
		got, err := GivingStreak(gifts, nil, periods, "ben", resume.Add(2*time.Hour), 0, nil, pauses)
		if err != nil || got.Current != 2 || got.Best != 2 || got.Since == nil || !got.Since.Equal(start) || got.AtRisk {
			t.Fatalf("%d-month pause: streak %+v, error %v", months, got, err)
		}
		got, err = GivingStreak(gifts[:1], nil, periods[:1], "ben", resume, 0, nil, pauses)
		if err != nil || got.Current != 1 || got.AtRisk {
			t.Fatalf("%d-month pause awaiting resumed invoice: %+v, %v", months, got, err)
		}
		if _, err = GivingStreak(gifts[:1], nil, periods[:1], "ben", resume.Add(Grace), 0, nil, pauses); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("%d-month pause missing resumed invoice after grace: %v", months, err)
		}
		periods[1].Status = "FAILED"
		got, err = GivingStreak(gifts[:1], nil, periods, "ben", resume.Add(time.Hour), 0, nil, pauses)
		if err != nil || got.Current != 1 || !got.AtRisk || got.GraceEndsAt == nil || !got.GraceEndsAt.Equal(resume.Add(Grace)) {
			t.Fatalf("%d-month pause failed resumed renewal: %+v, %v", months, got, err)
		}
		gifts[1].PaidAt = resume.Add(Grace)
		got, err = GivingStreak(gifts, nil, periods, "ben", resume.Add(Grace), 0, nil, pauses)
		if err != nil || got.Current != 1 || got.Since == nil || !got.Since.Equal(resume) {
			t.Fatalf("%d-month pause late recovery: %+v, %v", months, got, err)
		}
	}
}

func TestCancellationDuringPauseEndsContinuity(t *testing.T) {
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	paused, stop := start.AddDate(0, 1, 0), start.AddDate(0, 2, 0)
	periods := []Period{{ID: "first", DonorID: "ben", DueAt: start, EndsAt: paused, Status: "EXPECTED"}}
	gifts := []Gift{{ID: "first", DonorID: "ben", Amount: 3000, PaidAt: start, Frequency: "MONTHLY", PeriodID: "first", Currency: "USD"}}
	got, err := GivingStreak(gifts, nil, periods, "ben", stop, 0, []time.Time{stop}, []Pause{{Start: paused, End: paused.AddDate(0, 2, 0)}})
	if err != nil || got.Current != 0 || got.Best != 1 || got.Since != nil || got.AtRisk {
		t.Fatal("cancellation during pause retained the streak", got, err)
	}
}

func TestPauseFreezeUsesThreeCalendarMonths(t *testing.T) {
	start := time.Date(2027, 7, 31, 0, 0, 0, 0, time.UTC)
	paused := time.Date(2027, 8, 31, 0, 0, 0, 0, time.UTC)
	resume := time.Date(2027, 11, 30, 0, 0, 0, 0, time.UTC)
	periods := []Period{{ID: "first", DonorID: "ben", DueAt: start, EndsAt: paused, Status: "EXPECTED"}}
	gifts := []Gift{{ID: "first", DonorID: "ben", Amount: 3000, PaidAt: start, Frequency: "MONTHLY", PeriodID: "first", Currency: "USD"}}
	got, err := GivingStreak(gifts, nil, periods, "ben", resume.Add(-time.Second), 0, nil, []Pause{{Start: paused, End: resume}})
	if err != nil || got.Current != 1 || got.AtRisk {
		t.Fatal("three-renewal pause lapsed before resumption", got, err)
	}
}
