// Package recognition calculates donor recognition from verified financial facts.
// It does not ingest processor events or treat checkout attempts as payments.
package recognition

import (
	"errors"
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

// NewYork is the zone of the Founding Donor cutoff and receipt tax years.
var NewYork = mustLoadLocation("America/New_York")

// FoundingCutoff is exclusive: a first gift must succeed before it.
var FoundingCutoff = time.Date(2027, 4, 22, 0, 0, 0, 0, NewYork)

func mustLoadLocation(name string) *time.Location {
	zone, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return zone
}

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
	DonorID     string  `json:"donor_id"`
	FirstGiftID string  `json:"first_gift_id"`
	ReferrerID  *string `json:"referrer_id"`
	Kind        *string `json:"kind"`
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
		r := Relationship{DonorID: g.DonorID, FirstGiftID: g.ID}
		if c := g.Candidate; c != nil && c.ReferrerID != g.DonorID && g.PaidAt.Before(c.ExpiresAt) {
			r.ReferrerID = &c.ReferrerID
			r.Kind = &c.Kind
		}
		result[g.DonorID] = r
	}
	return result, nil
}

// FoundingQualifiedAt returns when donorID's first gift before the cutoff
// succeeded, if one had by asOf.
func FoundingQualifiedAt(input []Gift, donorID string, asOf time.Time) (time.Time, bool, error) {
	if asOf.IsZero() {
		return time.Time{}, false, ErrInvalid
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return time.Time{}, false, err
	}
	var first time.Time
	for _, g := range gifts {
		if g.DonorID == donorID && !g.PaidAt.After(asOf) && g.PaidAt.Before(FoundingCutoff) && (first.IsZero() || g.PaidAt.Before(first)) {
			first = g.PaidAt
		}
	}
	return first, !first.IsZero(), nil
}
func Founding(input []Gift, donorID string, asOf time.Time, awarded bool) (bool, error) {
	if asOf.IsZero() {
		return false, ErrInvalid
	}
	if awarded {
		return true, nil
	}
	_, qualified, err := FoundingQualifiedAt(input, donorID, asOf)
	return qualified, err
}

// GivingStreak counts consecutive paid monthly periods as of asOf. A renewal
// that is due but not yet observed keeps the current count for the grace
// window, after which the history is incomplete until the invoice arrives.
func GivingStreak(input []Gift, refunds []Refund, inputPeriods []Period, donorID string, asOf time.Time, previousBest int64, stops []time.Time, pauses []Pause) (Streak, error) {
	if previousBest < 0 {
		return Streak{}, ErrInvalid
	}
	gifts, err := cleanGifts(input)
	if err != nil {
		return Streak{}, err
	}
	net, err := NetGifts(gifts, refunds, asOf)
	if err != nil {
		return Streak{}, err
	}
	periods, err := donorPeriods(inputPeriods, donorID)
	if err != nil {
		return Streak{}, err
	}
	paid, err := periodPayments(gifts, net, periods, donorID)
	if err != nil {
		return Streak{}, err
	}
	stopped, err := stoppedRenewals(stops, pauses, asOf)
	if err != nil {
		return Streak{}, err
	}
	elapsed := []Period{}
	for _, p := range periods {
		if !p.DueAt.After(asOf) {
			elapsed = append(elapsed, p)
		}
	}
	if len(elapsed) == 0 {
		return Streak{Best: previousBest}, nil
	}
	last := elapsed[len(elapsed)-1]
	renewalDue := !last.EndsAt.After(asOf)
	if renewalDue && !stopped[last.EndsAt] && !asOf.Before(last.EndsAt.Add(Grace)) {
		return Streak{}, ErrIncomplete
	}
	result := countStreak(elapsed, paid, net, asOf)
	result.Best = max(previousBest, result.Best)
	if renewalDue && stopped[last.EndsAt] {
		result.Current = 0
		result.AtRisk = false
		result.GraceEndsAt = nil
	}
	return result, nil
}

// donorPeriods validates every period and returns donorID's in due order.
func donorPeriods(input []Period, donorID string) ([]Period, error) {
	seen := map[string]Period{}
	periods := []Period{}
	for _, p := range input {
		duration := p.EndsAt.Sub(p.DueAt)
		if p.ID == "" || p.DonorID == "" || p.DueAt.IsZero() || p.EndsAt.IsZero() || duration < 27*24*time.Hour || duration > 32*24*time.Hour || (p.Status != "EXPECTED" && p.Status != "FAILED") {
			return nil, ErrInvalid
		}
		p.DueAt = utc(p.DueAt)
		p.EndsAt = utc(p.EndsAt)
		if old, ok := seen[p.ID]; ok {
			if old != p {
				return nil, ErrInvalid
			}
			continue
		}
		seen[p.ID] = p
		if p.DonorID == donorID {
			periods = append(periods, p)
		}
	}
	sort.Slice(periods, func(i, j int) bool { return periods[i].DueAt.Before(periods[j].DueAt) })
	for i := 1; i < len(periods); i++ {
		if periods[i-1].EndsAt.After(periods[i].DueAt) {
			return nil, ErrInvalid
		}
	}
	return periods, nil
}

// periodPayments groups donorID's settled monthly gifts by billing period.
func periodPayments(gifts []Gift, net map[string]int64, periods []Period, donorID string) (map[string][]Gift, error) {
	known := map[string]bool{}
	for _, p := range periods {
		known[p.ID] = true
	}
	result := map[string][]Gift{}
	for _, g := range gifts {
		if g.DonorID != donorID || g.Frequency != "MONTHLY" {
			continue
		}
		if _, ok := net[g.ID]; !ok {
			continue
		}
		if !known[g.PeriodID] {
			return nil, ErrIncomplete
		}
		result[g.PeriodID] = append(result[g.PeriodID], g)
	}
	return result, nil
}

// stoppedRenewals returns the renewal boundaries skipped by cancellation or
// by a pause in effect at asOf.
func stoppedRenewals(stops []time.Time, pauses []Pause, asOf time.Time) (map[time.Time]bool, error) {
	stopped := map[time.Time]bool{}
	for _, t := range stops {
		if t.IsZero() {
			return nil, ErrInvalid
		}
		stopped[utc(t)] = true
	}
	for _, p := range pauses {
		if p.Start.IsZero() || p.End.IsZero() {
			return nil, ErrInvalid
		}
		if !asOf.Before(p.Start) && asOf.Before(p.End) {
			stopped[utc(p.Start)] = true
		}
	}
	return stopped, nil
}

func countStreak(elapsed []Period, payments map[string][]Gift, net map[string]int64, asOf time.Time) Streak {
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
		deadline := p.DueAt.Add(Grace)
		switch {
		case !paid.IsZero():
			if !paid.Before(deadline) || (!lastPaid.IsZero() && paid.Before(lastPaid)) {
				result.Current = 0
			}
			result.Current++
			lastPaid = paid
			result.Best = max(result.Best, result.Current)
		case len(payments[p.ID]) > 0 || !asOf.Before(deadline):
			result.Current = 0
			lastPaid = time.Time{}
		case result.Current > 0:
			result.AtRisk = true
			result.GraceEndsAt = &deadline
		}
		last = p.EndsAt
	}
	return result
}
