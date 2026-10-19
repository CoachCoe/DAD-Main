package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"dad/donor-api/internal/recognition"
)

// This deliberately exhaustive oracle reconstructs each historical snapshot independently.
func exhaustiveBest(h history, history []recognition.Refund, now time.Time, best int64) int64 {
	points := map[time.Time]bool{}
	for _, g := range h.Gifts {
		if !g.PaidAt.After(now) {
			points[g.PaidAt.UTC()] = true
		}
	}
	for _, r := range history {
		points[r.UpdatedAt.UTC()] = true
	}
	ordered := make([]time.Time, 0, len(points))
	for p := range points {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	for _, p := range ordered {
		latest := map[string]recognition.Refund{}
		for _, r := range history {
			if !r.UpdatedAt.After(p) {
				old, exists := latest[r.ID]
				if !exists || !r.UpdatedAt.Before(old.UpdatedAt) {
					latest[r.ID] = r
				}
			}
		}
		refunds := make([]recognition.Refund, 0, len(latest))
		for _, r := range latest {
			refunds = append(refunds, r)
		}
		v, err := recognition.GivingStreak(h.Gifts, refunds, h.Periods, "donor", p, best, nil, nil)
		if err == nil {
			best = max(best, v.Best)
		}
	}
	return best
}

func replayFixture(months int, random *rand.Rand) (history, []recognition.Refund, time.Time) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := history{}
	refunds := []recognition.Refund{}
	for i := range months {
		due, end := start.AddDate(0, i, 0), start.AddDate(0, i+1, 0)
		id := fmt.Sprint(i)
		h.Periods = append(h.Periods, recognition.Period{ID: id, DonorID: "donor", DueAt: due, EndsAt: end, Status: "EXPECTED"})
		if random != nil && random.IntN(5) == 0 {
			continue
		}
		delay := 0
		if random != nil {
			delay = random.IntN(20)
		}
		gift := recognition.Gift{
			ID:        "gift-" + id,
			DonorID:   "donor",
			PeriodID:  id,
			Amount:    3000,
			Currency:  "USD",
			Frequency: "MONTHLY",
			PaidAt:    due.Add(time.Duration(delay) * 24 * time.Hour),
		}
		h.Gifts = append(h.Gifts, gift)
		if random != nil && random.IntN(3) == 0 {
			r := recognition.Refund{
				ID:        "refund-" + id,
				GiftID:    gift.ID,
				Amount:    3000,
				Currency:  "USD",
				Status:    "PENDING",
				UpdatedAt: gift.PaidAt.Add(time.Hour),
			}
			if random.IntN(2) == 0 {
				r.Amount = 1000
			}
			refunds = append(refunds, r)
			r.Status = "SUCCEEDED"
			r.UpdatedAt = r.UpdatedAt.Add(24 * time.Hour)
			refunds = append(refunds, r)
		}
	}
	return h, refunds, start.AddDate(0, months, 0).Add(-time.Minute)
}

func TestReplayMatchesExhaustiveHistory(t *testing.T) {
	random := rand.New(rand.NewPCG(19, 73))
	for i := range 80 {
		h, refunds, now := replayFixture(24, random)
		// Input order must not determine financial recognition.
		random.Shuffle(len(h.Gifts), func(i, j int) { h.Gifts[i], h.Gifts[j] = h.Gifts[j], h.Gifts[i] })
		random.Shuffle(len(refunds), func(i, j int) { refunds[i], refunds[j] = refunds[j], refunds[i] })
		for _, previous := range []int64{0, 3, 24} {
			want := exhaustiveBest(h, refunds, now, previous)
			got, err := replayBest(context.Background(), "donor", h, refunds, now, previous, nil, nil)
			if err != nil || got != want {
				t.Fatalf("case %d prior %d: got %d, want %d: %v", i, previous, got, want, err)
			}
		}
	}
}

func TestReplayHonorsCancellation(t *testing.T) {
	h, r, now := replayFixture(120, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replayBest(ctx, "donor", h, r, now, 120, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func BenchmarkHistoricalBest(b *testing.B) {
	h, r, now := replayFixture(120, nil)
	for _, previous := range []int64{0, 120} {
		b.Run(fmt.Sprintf("prior-%d", previous), func(b *testing.B) {
			for b.Loop() {
				got, err := replayBest(context.Background(), "donor", h, r, now, previous, nil, nil)
				if err != nil || got != 120 {
					b.Fatal(got, err)
				}
			}
		})
	}
}

func TestReplayKeepsBestAcrossPauseBeforeAFullRefund(t *testing.T) {
	h, _, now := replayFixture(5, nil)
	pauses := []recognition.Pause{{Start: h.Periods[0].EndsAt, End: h.Periods[3].DueAt}}
	h.Periods = append(h.Periods[:1:1], h.Periods[3:]...)
	h.Gifts = append(h.Gifts[:1:1], h.Gifts[3:]...)
	r := recognition.Refund{ID: "refund", GiftID: h.Gifts[1].ID, Amount: 3000, Currency: "USD", Status: "SUCCEEDED", UpdatedAt: now}
	got, err := replayBest(context.Background(), "donor", h, []recognition.Refund{r}, now, 0, nil, pauses)
	if err != nil || got != 3 {
		t.Fatal("refund erased the pre-refund best across a pause", got, err)
	}
}
