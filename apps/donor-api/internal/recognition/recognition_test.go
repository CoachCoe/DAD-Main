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
				result, err = Founding(c.Gifts, c.DonorID, c.AsOf, c.PreviouslyAwarded)
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
