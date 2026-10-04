// Package recognition calculates donor recognition from verified financial facts.
// It does not ingest processor events or treat checkout attempts as payments.
package recognition

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"time"
	_ "time/tzdata"
)

var (
	ErrInvalid    = errors.New("invalid financial facts")
	ErrIncomplete = errors.New("incomplete billing history")
)

const Grace = 14 * 24 * time.Hour

type Candidate struct {
	ReferrerID string    `json:"referrer_id"`
	Kind       string    `json:"kind"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type Gift struct {
	ID        string     `json:"id"`
	DonorID   string     `json:"donor_id"`
	Amount    int64      `json:"amount_minor"`
	PaidAt    time.Time  `json:"paid_at"`
	Frequency string     `json:"frequency"`
	Candidate *Candidate `json:"candidate"`
	PeriodID  string     `json:"period_id"`
	Currency  string     `json:"currency"`
}
type Refund struct {
	ID        string    `json:"id"`
	GiftID    string    `json:"gift_id"`
	Amount    int64     `json:"amount_minor"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	Currency  string    `json:"currency"`
}
type Period struct {
	ID      string    `json:"id"`
	DonorID string    `json:"donor_id"`
	DueAt   time.Time `json:"due_at"`
	EndsAt  time.Time `json:"ends_at"`
	Status  string    `json:"status"`
}
type Relationship struct {
	DonorID     string    `json:"donor_id"`
	FirstGiftID string    `json:"first_gift_id"`
	ConvertedAt time.Time `json:"converted_at"`
	ReferrerID  *string   `json:"referrer_id"`
	Kind        *string   `json:"kind"`
}
type Totals struct {
	LinkCount int64  `json:"link_count"`
	IRLCount  int64  `json:"irl_count"`
	LinkMinor int64  `json:"link_minor"`
	IRLMinor  int64  `json:"irl_minor"`
	Currency  string `json:"currency"`
}
type Streak struct {
	Current     int64      `json:"current"`
	Best        int64      `json:"best"`
	AtRisk      bool       `json:"at_risk"`
	GraceEndsAt *time.Time `json:"grace_ends_at"`
}
type Pause struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func utc(t time.Time) time.Time { return t.Round(0).UTC() }
func cleanGifts(input []Gift) ([]Gift, error) {
	seen := map[string]Gift{}
	result := []Gift{}
	for _, g := range input {
		if g.ID == "" || g.DonorID == "" || g.Amount <= 0 || g.Currency != "USD" || g.PaidAt.IsZero() ||
			(g.Frequency != "MONTHLY" && g.Frequency != "ONE_TIME") || (g.Frequency == "ONE_TIME" && g.PeriodID != "") {
			return nil, ErrInvalid
		}
		g.PaidAt = utc(g.PaidAt)
		if g.Candidate != nil {
			c := *g.Candidate
			if c.ReferrerID == "" || (c.Kind != "LINK" && c.Kind != "IRL") || c.ExpiresAt.IsZero() {
				return nil, ErrInvalid
			}
			c.ExpiresAt = utc(c.ExpiresAt)
			g.Candidate = &c
		}
		if old, ok := seen[g.ID]; ok {
			if !reflect.DeepEqual(old, g) {
				return nil, ErrInvalid
			}
			continue
		}
		seen[g.ID] = g
		result = append(result, g)
	}
	return result, nil
}
func NetGifts(input []Gift, refunds []Refund, asOf time.Time) (map[string]int64, error) {
	if asOf.IsZero() {
		return nil, ErrInvalid
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return nil, err
	}
	byID := map[string]Gift{}
	net := map[string]int64{}
	seen := map[string]Refund{}
	for _, g := range gifts {
		byID[g.ID] = g
		if !g.PaidAt.After(asOf) {
			net[g.ID] = g.Amount
		}
	}
	for _, r := range refunds {
		if r.ID == "" || r.Amount <= 0 || r.Currency != "USD" || r.UpdatedAt.IsZero() {
			return nil, ErrInvalid
		}
		switch r.Status {
		case "PENDING", "REQUIRES_ACTION", "SUCCEEDED", "FAILED", "CANCELED":
		default:
			return nil, ErrInvalid
		}
		r.UpdatedAt = utc(r.UpdatedAt)
		if old, ok := seen[r.ID]; ok {
			if old != r {
				return nil, ErrInvalid
			}
			continue
		}
		seen[r.ID] = r
		g, ok := byID[r.GiftID]
		if !ok || r.UpdatedAt.After(asOf) || r.UpdatedAt.Before(g.PaidAt) || r.Amount > g.Amount {
			return nil, ErrInvalid
		}
		if r.Status == "SUCCEEDED" {
			if net[g.ID] < r.Amount {
				return nil, ErrInvalid
			}
			net[g.ID] -= r.Amount
		}
	}
	return net, nil
}
func Relationships(input []Gift, asOf time.Time) (map[string]Relationship, error) {
	if asOf.IsZero() {
		return nil, ErrInvalid
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return nil, err
	}
	sort.Slice(gifts, func(i, j int) bool {
		if gifts[i].PaidAt.Equal(gifts[j].PaidAt) {
			return gifts[i].ID < gifts[j].ID
		}
		return gifts[i].PaidAt.Before(gifts[j].PaidAt)
	})
	result := map[string]Relationship{}
	for _, g := range gifts {
		if _, ok := result[g.DonorID]; ok || g.PaidAt.After(asOf) {
			continue
		}
		r := Relationship{DonorID: g.DonorID, FirstGiftID: g.ID, ConvertedAt: g.PaidAt}
		if c := g.Candidate; c != nil && c.ReferrerID != g.DonorID && g.PaidAt.Before(c.ExpiresAt) {
			r.ReferrerID = &c.ReferrerID
			r.Kind = &c.Kind
		}
		result[g.DonorID] = r
	}
	return result, nil
}
func add(a, b int64) (int64, error) {
	if b > math.MaxInt64-a {
		return 0, ErrInvalid
	}
	return a + b, nil
}
func ReferralTotals(input []Gift, refunds []Refund, asOf time.Time) (map[string]Totals, error) {
	gifts, err := cleanGifts(input)
	if err != nil {
		return nil, err
	}
	net, err := NetGifts(gifts, refunds, asOf)
	if err != nil {
		return nil, err
	}
	relations, err := Relationships(gifts, asOf)
	if err != nil {
		return nil, err
	}
	raised := map[string]int64{}
	result := map[string]Totals{}
	for _, g := range gifts {
		raised[g.DonorID], err = add(raised[g.DonorID], net[g.ID])
		if err != nil {
			return nil, err
		}
	}
	for id, r := range relations {
		if r.ReferrerID == nil {
			continue
		}
		t := result[*r.ReferrerID]
		t.Currency = "USD"
		amount := raised[id]
		if *r.Kind == "LINK" {
			if amount > 0 {
				t.LinkCount++
			}
			t.LinkMinor, err = add(t.LinkMinor, amount)
		} else {
			if amount > 0 {
				t.IRLCount++
			}
			t.IRLMinor, err = add(t.IRLMinor, amount)
		}
		if err != nil {
			return nil, err
		}
		if _, err = add(t.LinkMinor, t.IRLMinor); err != nil {
			return nil, err
		}
		result[*r.ReferrerID] = t
	}
	return result, nil
}
func Founding(input []Gift, donorID string, asOf time.Time, awarded bool) (bool, error) {
	if asOf.IsZero() {
		return false, ErrInvalid
	}
	if awarded {
		return true, nil
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return false, err
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		return false, err
	}
	cutoff := time.Date(2027, 4, 22, 0, 0, 0, 0, zone)
	for _, g := range gifts {
		if g.DonorID == donorID && !g.PaidAt.After(asOf) && g.PaidAt.Before(cutoff) {
			return true, nil
		}
	}
	return false, nil
}
func GivingStreak(input []Gift, refunds []Refund, inputPeriods []Period, donorID string, asOf time.Time, previousBest int64, stops []time.Time, pauses []Pause) (Streak, error) {
	fail := func(err error) (Streak, error) { return Streak{}, err }
	if previousBest < 0 {
		return fail(ErrInvalid)
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return fail(err)
	}
	net, err := NetGifts(gifts, refunds, asOf)
	if err != nil {
		return fail(err)
	}
	seen := map[string]Period{}
	periods := []Period{}
	byID := map[string]Period{}
	for _, p := range inputPeriods {
		duration := p.EndsAt.Sub(p.DueAt)
		if p.ID == "" || p.DonorID == "" || p.DueAt.IsZero() || p.EndsAt.IsZero() || duration < 27*24*time.Hour || duration > 32*24*time.Hour {
			return fail(ErrInvalid)
		}
		switch p.Status {
		case "EXPECTED", "FAILED", "SKIPPED":
		default:
			return fail(ErrInvalid)
		}
		p.DueAt = utc(p.DueAt)
		p.EndsAt = utc(p.EndsAt)
		if old, ok := seen[p.ID]; ok {
			if old != p {
				return fail(ErrInvalid)
			}
			continue
		}
		seen[p.ID] = p
		if p.DonorID == donorID {
			periods = append(periods, p)
			byID[p.ID] = p
		}
	}
	sort.Slice(periods, func(i, j int) bool { return periods[i].DueAt.Before(periods[j].DueAt) })
	for i := 1; i < len(periods); i++ {
		if periods[i-1].EndsAt.After(periods[i].DueAt) {
			return fail(ErrInvalid)
		}
	}
	payments := map[string][]Gift{}
	for _, g := range gifts {
		if g.DonorID != donorID || g.Frequency != "MONTHLY" {
			continue
		}
		if _, ok := net[g.ID]; !ok {
			continue
		}
		p, ok := byID[g.PeriodID]
		if !ok {
			return fail(ErrIncomplete)
		}
		if p.Status == "SKIPPED" {
			return fail(ErrInvalid)
		}
		payments[p.ID] = append(payments[p.ID], g)
	}
	stopped := map[time.Time]bool{}
	for _, t := range stops {
		if t.IsZero() {
			return fail(ErrInvalid)
		}
		stopped[utc(t)] = true
	}
	for _, p := range pauses {
		if p.Start.IsZero() || p.End.IsZero() {
			return fail(ErrInvalid)
		}
		if !asOf.Before(p.Start) && asOf.Before(p.End) {
			stopped[utc(p.Start)] = true
		}
	}
	elapsed := []Period{}
	for _, p := range periods {
		if !p.DueAt.After(asOf) {
			elapsed = append(elapsed, p)
		}
	}
	if len(elapsed) > 0 {
		last := elapsed[len(elapsed)-1]
		if !last.EndsAt.After(asOf) && last.Status != "SKIPPED" && !stopped[last.EndsAt] {
			return fail(ErrIncomplete)
		}
	}
	result := Streak{}
	var last, lastPaid time.Time
	for _, p := range elapsed {
		if !last.Equal(p.DueAt) {
			result.Current = 0
		}
		var paid time.Time
		for _, g := range payments[p.ID] {
			if net[g.ID] > 0 && (paid.IsZero() || g.PaidAt.Before(paid)) {
				paid = g.PaidAt
			}
		}
		result.AtRisk = false
		result.GraceEndsAt = nil
		if !paid.IsZero() {
			if !paid.Before(p.DueAt.Add(Grace)) || (!lastPaid.IsZero() && paid.Before(lastPaid)) {
				result.Current = 0
			}
			result.Current++
			lastPaid = paid
			result.Best = max(result.Best, result.Current)
		} else if p.Status == "SKIPPED" || len(payments[p.ID]) > 0 || !asOf.Before(p.DueAt.Add(Grace)) {
			result.Current = 0
			lastPaid = time.Time{}
		} else if result.Current > 0 {
			result.AtRisk = true
			deadline := p.DueAt.Add(Grace)
			result.GraceEndsAt = &deadline
		}
		last = p.EndsAt
	}
	result.Best = max(previousBest, result.Best)
	if len(elapsed) > 0 {
		last := elapsed[len(elapsed)-1]
		if !last.EndsAt.After(asOf) && stopped[last.EndsAt] {
			result.Current = 0
			result.AtRisk = false
			result.GraceEndsAt = nil
		}
	}
	return result, nil
}
