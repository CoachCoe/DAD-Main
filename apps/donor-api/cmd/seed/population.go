package main

import (
	"container/heap"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/recognition"
)

// emailDomain marks seeded donors. The reserved .invalid TLD can never
// receive mail, and the guard treats any other address as real data.
const emailDomain = "loadtest.invalid"

const (
	day            = 24 * time.Hour
	history        = 730 * day // the oldest donor signed up two years ago
	referralWindow = 30 * day
)

// Shares of the population and per-event rates. They describe a referral-led
// launch: most donors give monthly at the preselected $30, a minority share a
// code, and sharing influence is heavy-tailed.
const (
	abandonedShare       = 0.08 // started checkout, never paid
	oneTimeShare         = 0.22
	referredShare        = 0.45 // arrived through a referral touch
	lateTouchShare       = 0.03 // of referred: the window had lapsed by checkout
	irlShare             = 0.35
	claimShare           = 0.60 // givers who claim an account
	sharerShare          = 0.40 // claimed donors who choose a public name
	preferenceShare      = 0.30 // claimed donors who saved notification choices
	extraGiftShare       = 0.06 // monthly donors with an additional one-time gift
	renewalFailure       = 0.05
	cancelHazard         = 0.015 // per renewal, cancelled by the donor at period end
	scheduledCancelShare = 0.02  // of live subscriptions, ending at the current period's end
	pauseShare           = 0.04
	unreportedShare      = 0.15 // of renewals due in the last 14 days
	refundShare          = 0.015
	influenceAlpha       = 1.16 // Pareto shape: a few sharers refer hundreds
	influenceCeiling     = 3000
)

// Gift amounts in cents by weight; 0 draws a custom whole-dollar amount.
var monthlyAmounts = weighted[int64]{{3000, 60}, {1000, 12}, {5000, 12}, {10000, 8}, {9000, 3}, {15000, 2}, {0, 3}}
var oneTimeAmounts = weighted[int64]{{3000, 30}, {5000, 20}, {10000, 20}, {1000, 10}, {15000, 5}, {9000, 5}, {0, 10}}

var names = []string{"Ada", "Amara", "Ben", "Carmen", "Chen", "Dara", "Eli", "Fatima", "Gabe", "Hana", "Ines", "Jamal",
	"Kai", "Lena", "Mateo", "Mira", "Nadia", "Omar", "Priya", "Quinn", "Rosa", "Sam", "Tariq", "Uma", "Vera", "Wes",
	"Xin", "Yara", "Zane", "Ana", "Bo", "Cleo", "Dev", "Emi", "Finn", "Gia", "Hugo", "Iris", "Jo", "Kofi"}

type weighted[T any] []struct {
	value  T
	weight float64
}

func (w weighted[T]) pick(r *rand.Rand) T {
	total := 0.0
	for _, option := range w {
		total += option.weight
	}
	x := r.Float64() * total
	for _, option := range w {
		if x -= option.weight; x < 0 {
			return option.value
		}
	}
	return w[len(w)-1].value
}

type donor struct {
	index      int
	id         pgtype.UUID
	email      string
	createdAt  time.Time
	claimedAt  time.Time // zero while unclaimed
	sharingAt  time.Time // when the public name and code were chosen
	publicName string
	code       string
	influence  float64
	preference bool
	checkouts  []*checkout

	// Expected dashboard figures, derived from how the facts were generated
	// rather than by the projection code, so tests can compare the two.
	referred    map[string]*referralTally
	streak      int
	atRisk      bool
	streakKnown bool
}

type referralTally struct{ count, minor int64 }

type checkout struct {
	id, attempt  pgtype.UUID
	donor        *donor
	amount       int64
	frequency    string
	createdAt    time.Time
	referrer     *donor
	kind         string
	expiresAt    time.Time
	status       string // attempt status: EXPIRED, COMPLETE or CANCELLED
	session      string
	subscription string
	periods      []*period
	gifts        []*gift
	sub          *subscription
	pause        *pause
	cancellation *cancellation
}

type period struct {
	id         pgtype.UUID
	invoice    string
	due, ends  time.Time
	paid       bool
	failedAt   time.Time // zero unless a renewal attempt failed
	paidAt     time.Time
	recordedAt time.Time
}

type gift struct {
	id            pgtype.UUID
	object        string
	paymentIntent string
	amount        int64
	frequency     string
	paidAt        time.Time
	period        *period
	referrer      *donor
	kind          string
	refund        *refund
}

type refund struct {
	id           pgtype.UUID
	stripeID     string
	amount       int64
	status       string
	observations []refundObservation
}

type refundObservation struct {
	status string
	at     time.Time
}

type subscription struct {
	id        pgtype.UUID
	status    string
	periodEnd time.Time
	stopsAt   time.Time // zero when renewals continue
	observed  time.Time
}

type pause struct {
	id                 pgtype.UUID
	months             int
	starts, resumes    time.Time
	created, confirmed time.Time
}

type cancellation struct {
	id        pgtype.UUID
	periodEnd time.Time
	created   time.Time
	completed time.Time
}

// touch is a referral visit, whether or not it led to a gift.
type touch struct {
	id        pgtype.UUID
	tokenHash string
	referrer  *donor
	kind      string
	at        time.Time
}

type population struct {
	now     time.Time
	donors  []*donor
	touches []touch
	ids     *rand.Rand // identifiers for rows the model does not track
}

type generator struct {
	r   *rand.Rand
	now time.Time
	pop *population
}

// generate builds a deterministic population of n donors as of now. offset
// numbers the donors after those an earlier run created, so appended
// populations get distinct emails and identifiers.
func generate(n, offset int, seed uint64, now time.Time) *population {
	now = now.UTC().Truncate(time.Second)
	g := &generator{r: rand.New(rand.NewPCG(seed, uint64(offset))), now: now}
	g.pop = &population{now: now, ids: rand.New(rand.NewPCG(seed, ^uint64(offset)))}
	ages := make([]time.Duration, n)
	for i := range ages {
		// Squaring skews signups toward the present, as referral growth compounds.
		u := g.r.Float64()
		ages[i] = max(time.Hour, time.Duration(u*u*float64(history)).Truncate(time.Second))
	}
	slices.SortFunc(ages, func(a, b time.Duration) int { return int(b - a) })
	sharers := &sharerPool{}
	for i, age := range ages {
		d := &donor{index: offset + i, id: g.uuid(), createdAt: now.Add(-age), streakKnown: true}
		d.email = fmt.Sprintf("donor-%07d@%s", d.index, emailDomain)
		g.pop.donors = append(g.pop.donors, d)
		g.join(d, sharers)
	}
	g.visits()
	g.expectReferrals()
	return g.pop
}

// join gives d a first checkout, possibly referred by an active sharer, and
// the giving history that followed.
func (g *generator) join(d *donor, sharers *sharerPool) {
	c := &checkout{id: g.uuid(), attempt: g.uuid(), donor: d, createdAt: d.createdAt}
	if g.r.Float64() < referredShare {
		lapsed := g.r.Float64() < lateTouchShare
		at := d.createdAt.Add(-g.seconds(min(25*day, time.Duration(g.r.ExpFloat64()*float64(18*time.Hour)))))
		if lapsed {
			at = d.createdAt.Add(-referralWindow - g.seconds(30*day))
		}
		sharers.activate(at)
		if referrer := sharers.pick(g.r, at); referrer != nil {
			kind := g.kind()
			g.pop.touches = append(g.pop.touches, touch{g.uuid(), g.hash(), referrer, kind, at})
			if !lapsed {
				c.referrer, c.kind, c.expiresAt = referrer, kind, at.Add(referralWindow)
			}
		}
	}
	d.checkouts = append(d.checkouts, c)
	switch x := g.r.Float64(); {
	case x < abandonedShare:
		c.amount, c.frequency, c.status = g.amount(monthlyAmounts), "MONTHLY", "EXPIRED"
		return
	case x < abandonedShare+oneTimeShare:
		c.amount, c.frequency, c.status = g.amount(oneTimeAmounts), "ONE_TIME", "COMPLETE"
		g.oneTime(c, d.createdAt.Add(g.seconds(10*time.Minute)))
	default:
		c.amount, c.frequency, c.status = g.amount(monthlyAmounts), "MONTHLY", "COMPLETE"
		g.monthly(c)
		if g.r.Float64() < extraGiftShare {
			extra := &checkout{id: g.uuid(), attempt: g.uuid(), donor: d, amount: g.amount(oneTimeAmounts), frequency: "ONE_TIME", status: "COMPLETE"}
			extra.createdAt = d.createdAt.Add(g.seconds(g.now.Sub(d.createdAt) - 15*time.Minute))
			d.checkouts = append(d.checkouts, extra)
			g.oneTime(extra, extra.createdAt.Add(g.seconds(10*time.Minute)))
		}
	}
	first := c.gifts[0].paidAt
	if g.r.Float64() < claimShare {
		if at := first.Add(g.seconds(min(7*day, time.Duration(g.r.ExpFloat64()*float64(6*time.Hour))))); at.Before(g.now) {
			d.claimedAt = at
			d.preference = g.r.Float64() < preferenceShare
		}
	}
	if !d.claimedAt.IsZero() && g.r.Float64() < sharerShare {
		if at := d.claimedAt.Add(g.seconds(2 * day)); at.Before(g.now) {
			d.sharingAt, d.publicName, d.code = at, names[g.r.IntN(len(names))], g.code()
			d.influence = min(influenceCeiling, math.Pow(1-g.r.Float64(), -1/influenceAlpha))
			sharers.add(d)
		}
	}
	for _, c := range d.checkouts {
		for _, gift := range c.gifts {
			g.maybeRefund(gift)
		}
	}
	if d.streakKnown {
		d.streakKnown = !hasFullMonthlyRefund(d)
	}
}

func (g *generator) oneTime(c *checkout, paid time.Time) {
	c.session = g.stripeID("cs_test_")
	pi := g.stripeID("pi_")
	g.addGift(c, &gift{id: g.uuid(), object: pi, paymentIntent: pi, amount: c.amount, frequency: "ONE_TIME", paidAt: paid})
}

func (g *generator) addGift(c *checkout, gift *gift) {
	if c.referrer != nil && gift.paidAt.Before(c.expiresAt) {
		gift.referrer, gift.kind = c.referrer, c.kind
	}
	c.gifts = append(c.gifts, gift)
}

// monthly bills renewals on the subscription's anniversary until now, with
// failures recovered within or after grace, unrecovered failures, donor
// cancellations, pauses, and renewals the processor has not reported yet.
func (g *generator) monthly(c *checkout) {
	d := c.donor
	c.session, c.subscription = g.stripeID("cs_test_"), g.stripeID("sub_")
	anchor := c.createdAt.Add(g.seconds(5 * time.Minute))
	c.sub = &subscription{id: g.uuid(), status: "active"}
	pauseAt := -1
	if g.r.Float64() < pauseShare {
		pauseAt = 2 + g.r.IntN(12)
	}
	var lastEnds time.Time
	run := 0
	for k := 0; ; k++ {
		due, ends := addMonths(anchor, k), addMonths(anchor, k+1)
		if due.After(g.now) {
			break
		}
		if k == pauseAt {
			months := 1 + g.r.IntN(3)
			created := due.Add(-g.seconds(10 * day))
			c.pause = &pause{id: g.uuid(), months: months, starts: due, resumes: addMonths(anchor, k+months), created: created, confirmed: created.Add(time.Minute)}
			k += months - 1
			continue
		}
		// A cancellation or a late report straight after a pause would leave the
		// pause start as the last renewal, which recognition reads as unknown.
		continuous := due.Equal(lastEnds)
		if continuous && g.r.Float64() < cancelHazard {
			created := due.Add(-g.seconds(20 * day))
			c.cancellation = &cancellation{id: g.uuid(), periodEnd: due, created: created, completed: created.Add(time.Minute)}
			c.sub.stopsAt = due
			break
		}
		if continuous && g.now.Sub(due) < recognition.Grace && g.r.Float64() < unreportedShare {
			break
		}
		p := &period{id: g.uuid(), invoice: g.stripeID("in_"), due: due, ends: ends}
		processorCancels := false
		switch {
		case k == 0:
			p.paidAt = due.Add(g.seconds(time.Minute))
		case g.r.Float64() >= renewalFailure:
			p.paidAt = due.Add(time.Minute + g.seconds(90*time.Minute))
		default:
			p.failedAt = due.Add(g.seconds(time.Hour))
			switch x := g.r.Float64(); {
			case x < 0.60:
				p.paidAt = due.Add(day + g.seconds(12*day))
			case x < 0.75:
				p.paidAt = due.Add(15*day + g.seconds(10*day))
			default:
				processorCancels = g.r.Float64() < 0.5
			}
		}
		if p.failedAt.After(g.now) {
			p.failedAt = time.Time{}
		}
		if p.paidAt.After(g.now) {
			p.paidAt = time.Time{}
		}
		if p.paid = !p.paidAt.IsZero(); p.paid {
			g.addGift(c, &gift{id: g.uuid(), object: p.invoice, paymentIntent: g.stripeID("pi_"), amount: c.amount, frequency: "MONTHLY", paidAt: p.paidAt, period: p})
		}
		if !p.paid && p.failedAt.IsZero() {
			break // the processor has reported neither outcome yet
		}
		p.recordedAt = p.failedAt
		if p.paid {
			p.recordedAt = p.paidAt
		}
		c.periods = append(c.periods, p)
		if !due.Equal(lastEnds) {
			run = 0
		}
		d.atRisk = false
		switch {
		case p.paid && p.paidAt.Before(due.Add(recognition.Grace)):
			run++
		case p.paid:
			run = 1
		case !g.now.Before(due.Add(recognition.Grace)):
			run = 0
		case run > 0:
			d.atRisk = true
		}
		lastEnds = ends
		if processorCancels && !ends.After(g.now) {
			c.sub.status, c.sub.stopsAt = "canceled", ends
			break
		}
	}
	last := c.periods[len(c.periods)-1]
	c.sub.periodEnd, c.sub.observed = last.ends, last.recordedAt
	if c.sub.status == "active" && !last.paid {
		c.sub.status = "past_due"
	}
	if c.sub.stopsAt.IsZero() && last.ends.After(g.now) && g.r.Float64() < scheduledCancelShare {
		created := last.due.Add(g.seconds(g.now.Sub(last.due)))
		c.cancellation = &cancellation{id: g.uuid(), periodEnd: last.ends, created: created, completed: created.Add(time.Minute)}
		c.sub.stopsAt = last.ends
	}
	if !c.sub.stopsAt.IsZero() && !c.sub.stopsAt.After(g.now) {
		c.sub.status, c.status = "canceled", "CANCELLED"
		c.sub.periodEnd = c.sub.stopsAt
	}
	stopped := !c.sub.stopsAt.IsZero() && !c.sub.stopsAt.After(g.now) && c.sub.stopsAt.Equal(lastEnds)
	paused := c.pause != nil && !g.now.Before(c.pause.starts) && g.now.Before(c.pause.resumes) && c.pause.starts.Equal(lastEnds)
	if stopped || paused {
		run, d.atRisk = 0, false
	}
	d.streak = run
}

func (g *generator) maybeRefund(gift *gift) {
	if g.r.Float64() >= refundShare {
		return
	}
	requested := gift.paidAt.Add(time.Hour + g.seconds(20*day))
	if requested.After(g.now) {
		return
	}
	amount := gift.amount
	if g.r.Float64() < 0.3 {
		amount = max(1, gift.amount/2)
	}
	r := &refund{id: g.uuid(), stripeID: g.stripeID("re_"), amount: amount, status: "PENDING"}
	r.observations = append(r.observations, refundObservation{"PENDING", requested})
	settled := requested.Add(time.Hour + g.seconds(5*day))
	if !settled.After(g.now) {
		r.status = weighted[string]{{"SUCCEEDED", 85}, {"FAILED", 5}, {"CANCELED", 2}, {"PENDING", 8}}.pick(g.r)
		if r.status != "PENDING" {
			r.observations = append(r.observations, refundObservation{r.status, settled})
		}
	}
	gift.refund = r
}

func hasFullMonthlyRefund(d *donor) bool {
	for _, c := range d.checkouts {
		for _, g := range c.gifts {
			if g.frequency == "MONTHLY" && g.refund != nil && g.refund.status == "SUCCEEDED" && g.refund.amount == g.amount {
				return true
			}
		}
	}
	return false
}

// visits adds the referral touches that did not lead to a gift: several per
// conversion, plus a few for every sharer.
func (g *generator) visits() {
	conversions := map[*donor]int{}
	for _, t := range g.pop.touches {
		conversions[t.referrer]++
	}
	for _, d := range g.pop.donors {
		if d.code == "" {
			continue
		}
		visits := g.r.IntN(3)
		for range conversions[d] {
			visits += int(g.r.ExpFloat64() * 6)
		}
		for range visits {
			at := d.sharingAt.Add(g.seconds(g.now.Sub(d.sharingAt)))
			g.pop.touches = append(g.pop.touches, touch{g.uuid(), g.hash(), d, g.kind(), at})
		}
	}
}

// expectReferrals tallies, for each referrer, the donors whose first gift
// carried their attribution, as the dashboard should report them.
func (g *generator) expectReferrals() {
	for _, d := range g.pop.donors {
		first := d.firstGift()
		if first == nil || first.referrer == nil {
			continue
		}
		r := first.referrer
		if r.referred == nil {
			r.referred = map[string]*referralTally{"LINK": {}, "IRL": {}}
		}
		net := d.net()
		tally := r.referred[first.kind]
		tally.minor += net
		if net > 0 {
			tally.count++
		}
	}
}

func (d *donor) gifts() []*gift {
	var out []*gift
	for _, c := range d.checkouts {
		out = append(out, c.gifts...)
	}
	return out
}

func (d *donor) firstGift() *gift {
	var first *gift
	for _, g := range d.gifts() {
		if first == nil || g.paidAt.Before(first.paidAt) {
			first = g
		}
	}
	return first
}

func (d *donor) net() int64 {
	total := int64(0)
	for _, g := range d.gifts() {
		total += g.amount
		if g.refund != nil && g.refund.status == "SUCCEEDED" {
			total -= g.refund.amount
		}
	}
	return total
}

// addMonths returns t moved by months, on the same day of the month or the
// month's last day, the way the processor dates monthly renewals.
func addMonths(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	return first.AddDate(0, 0, min(d, first.AddDate(0, 1, -1).Day())-1)
}

func (g *generator) amount(choices weighted[int64]) int64 {
	if amount := choices.pick(g.r); amount != 0 {
		return amount
	}
	return int64(5+g.r.IntN(496)) * 100
}

func (g *generator) kind() string {
	if g.r.Float64() < irlShare {
		return "IRL"
	}
	return "LINK"
}

// seconds returns a uniform whole-second duration in [0, limit).
func (g *generator) seconds(limit time.Duration) time.Duration {
	if limit < time.Second {
		return 0
	}
	return time.Duration(g.r.Int64N(int64(limit/time.Second))) * time.Second
}

func (g *generator) bytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.r.Uint32())
	}
	return b
}

func (g *generator) uuid() pgtype.UUID {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], g.bytes(16))
	id.Bytes[6] = (id.Bytes[6] & 0x0f) | 0x40
	id.Bytes[8] = (id.Bytes[8] & 0x3f) | 0x80
	return id
}

// code has the shape of an issued referral code: 24 bytes of URL-safe base64.
func (g *generator) code() string { return base64.RawURLEncoding.EncodeToString(g.bytes(24)) }

// hash stands in for a visitor token's hash; nobody holds the token.
func (g *generator) hash() string { return hex.EncodeToString(g.bytes(32)) }

func (g *generator) stripeID(prefix string) string { return prefix + hex.EncodeToString(g.bytes(12)) }

// sharerPool samples referrers in proportion to influence among donors whose
// code existed at the time of the visit.
type sharerPool struct {
	pending pendingSharers
	active  []*donor
	totals  []float64
}

func (p *sharerPool) add(d *donor) { heap.Push(&p.pending, d) }

func (p *sharerPool) activate(at time.Time) {
	for len(p.pending) > 0 && p.pending[0].sharingAt.Before(at) {
		d := heap.Pop(&p.pending).(*donor)
		total := d.influence
		if n := len(p.totals); n > 0 {
			total += p.totals[n-1]
		}
		p.active, p.totals = append(p.active, d), append(p.totals, total)
	}
}

// pick returns a sharer whose code existed at the visit. Visits arrive
// roughly, not strictly, in time order, so a recently activated sharer may be
// too new and is drawn again.
func (p *sharerPool) pick(r *rand.Rand, at time.Time) *donor {
	for range 8 {
		if len(p.active) == 0 {
			return nil
		}
		d := p.active[sort.SearchFloat64s(p.totals, r.Float64()*p.totals[len(p.totals)-1])]
		if d.sharingAt.Before(at) {
			return d
		}
	}
	return nil
}

type pendingSharers []*donor

func (h pendingSharers) Len() int           { return len(h) }
func (h pendingSharers) Less(i, j int) bool { return h[i].sharingAt.Before(h[j].sharingAt) }
func (h pendingSharers) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *pendingSharers) Push(x any)        { *h = append(*h, x.(*donor)) }
func (h *pendingSharers) Pop() any {
	old := *h
	d := old[len(old)-1]
	*h = old[:len(old)-1]
	return d
}

// shape summarises the generated population for the seed report.
type shape struct {
	Abandoned      int            `json:"abandoned"`
	OneTime        int            `json:"one_time"`
	Monthly        int            `json:"monthly"`
	MonthlyAmounts map[string]int `json:"monthly_amounts"`
	Claimed        int            `json:"claimed"`
	Sharers        int            `json:"sharers"`
	Referred       int            `json:"referred"`
	Referrers      map[string]int `json:"referrers_by_referrals"`
	MostReferrals  int64          `json:"most_referrals"`
	Touches        int            `json:"touches"`
	Gifts          int            `json:"gifts"`
	Periods        int            `json:"periods"`
	FailedRenewals int            `json:"failed_renewals"`
	Refunds        int            `json:"refunds"`
	Paused         int            `json:"paused"`
	Cancelled      int            `json:"cancelled"`
	PastDue        int            `json:"past_due"`
	AtRisk         int            `json:"at_risk"`
	LongestStreak  int            `json:"longest_streak"`
}

func (p *population) shape() shape {
	s := shape{MonthlyAmounts: map[string]int{}, Referrers: map[string]int{}, Touches: len(p.touches)}
	for _, d := range p.donors {
		switch first := d.checkouts[0]; {
		case first.status == "EXPIRED":
			s.Abandoned++
		case first.frequency == "ONE_TIME":
			s.OneTime++
		default:
			s.Monthly++
			s.MonthlyAmounts[amountLabel(first.amount)]++
		}
		if !d.claimedAt.IsZero() {
			s.Claimed++
		}
		if d.code != "" {
			s.Sharers++
		}
		if g := d.firstGift(); g != nil && g.referrer != nil {
			s.Referred++
		}
		if d.atRisk {
			s.AtRisk++
		}
		s.LongestStreak = max(s.LongestStreak, d.streak)
		if n := d.referrals(); n > 0 {
			s.MostReferrals = max(s.MostReferrals, n)
			s.Referrers[referralBucket(n)]++
		}
		for _, c := range d.checkouts {
			s.Gifts += len(c.gifts)
			s.Periods += len(c.periods)
			for _, pd := range c.periods {
				if !pd.failedAt.IsZero() {
					s.FailedRenewals++
				}
			}
			for _, g := range c.gifts {
				if g.refund != nil {
					s.Refunds++
				}
			}
			if c.pause != nil {
				s.Paused++
			}
			if c.sub != nil && c.sub.status == "canceled" {
				s.Cancelled++
			}
			if c.sub != nil && c.sub.status == "past_due" {
				s.PastDue++
			}
		}
	}
	return s
}

// referrals is how many donors d's dashboard should count as referred.
func (d *donor) referrals() int64 {
	var n int64
	for _, tally := range d.referred {
		n += tally.count
	}
	return n
}

func amountLabel(minor int64) string {
	for _, preset := range monthlyAmounts {
		if preset.value == minor {
			return fmt.Sprintf("$%d", minor/100)
		}
	}
	return "custom"
}

func referralBucket(n int64) string {
	switch {
	case n >= 100:
		return "100+"
	case n >= 10:
		return "10-99"
	case n >= 2:
		return "2-9"
	}
	return "1"
}
