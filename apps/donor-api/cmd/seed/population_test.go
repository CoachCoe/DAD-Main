package main

import (
	"math"
	"testing"
	"time"
)

var asOf = time.Date(2026, 10, 7, 15, 4, 5, 0, time.UTC)

func TestGenerateIsDeterministic(t *testing.T) {
	a, b, other := generate(500, 0, 9, asOf), generate(500, 0, 9, asOf), generate(500, 0, 10, asOf)
	differs := false
	for i := range a.donors {
		x, y := a.donors[i], b.donors[i]
		if x.id != y.id || x.email != y.email || x.code != y.code || len(x.gifts()) != len(y.gifts()) || x.streak != y.streak ||
			x.net() != y.net() {
			t.Fatalf("donor %d differs between runs with the same seed", i)
		}
		differs = differs || x.id != other.donors[i].id
	}
	if !differs || len(a.touches) != len(b.touches) {
		t.Fatal("seed does not determine the population")
	}
	appended := generate(10, 500, 9, asOf)
	if appended.donors[0].email != "donor-0000500@"+emailDomain || appended.donors[0].id == a.donors[0].id {
		t.Fatal("an appended population must continue the numbering with new identifiers")
	}
}

func TestPopulationHasALaunchShape(t *testing.T) {
	pop := generate(50000, 0, 1, asOf)
	var oneTime, abandoned, monthly, monthly30, referred, sharers, referrers, heavy, periods, failed int
	var mostReferrals int64
	var longest, atRisk, paused, cancelled, pastDue, refunds int
	for _, d := range pop.donors {
		switch first := d.checkouts[0]; {
		case first.status == "EXPIRED":
			abandoned++
		case first.frequency == "ONE_TIME":
			oneTime++
		default:
			monthly++
			if first.amount == 3000 {
				monthly30++
			}
		}
		if g := d.firstGift(); g != nil && g.referrer != nil {
			referred++
		}
		if d.code != "" {
			sharers++
		}
		if n := d.referrals(); n > 0 {
			referrers++
			mostReferrals = max(mostReferrals, n)
			if n >= 100 {
				heavy++
			}
		}
		longest = max(longest, d.streak)
		if d.atRisk {
			atRisk++
		}
		for _, c := range d.checkouts {
			periods += len(c.periods)
			for _, p := range c.periods {
				if !p.failedAt.IsZero() {
					failed++
				}
			}
			for _, g := range c.gifts {
				if g.refund != nil {
					refunds++
				}
			}
			if c.pause != nil {
				paused++
			}
			if c.sub != nil && c.sub.status == "canceled" {
				cancelled++
			} else if c.sub != nil && c.sub.status == "past_due" {
				pastDue++
			}
		}
	}
	share := func(n int) float64 { return float64(n) / float64(len(pop.donors)) }
	for name, check := range map[string]struct{ got, want, tolerance float64 }{
		"monthly":      {share(monthly), 1 - abandonedShare - oneTimeShare, 0.02},
		"one-time":     {share(oneTime), oneTimeShare, 0.02},
		"abandoned":    {share(abandoned), abandonedShare, 0.01},
		"$30 monthly":  {float64(monthly30) / float64(monthly), 0.60, 0.02},
		"referred":     {share(referred), 0.40, 0.08},
		"sharers":      {share(sharers), 0.92 * claimShare * sharerShare, 0.05},
		"failed rates": {float64(failed) / float64(periods), renewalFailure, 0.01},
	} {
		if math.Abs(check.got-check.want) > check.tolerance {
			t.Errorf("%s share %.3f, want %.3f±%.3f", name, check.got, check.want, check.tolerance)
		}
	}
	if share(referrers) > 0.2 || heavy == 0 || mostReferrals < 200 {
		t.Errorf("referrals are not heavy-tailed: %d referrers, %d with 100+, most %d", referrers, heavy, mostReferrals)
	}
	if longest < 20 || atRisk == 0 || paused == 0 || cancelled == 0 || pastDue == 0 || refunds == 0 {
		t.Errorf(
			"missing recognition edge cases: longest streak %d, %d at risk, %d paused, %d cancelled, %d past due, %d refunds",
			longest,
			atRisk,
			paused,
			cancelled,
			pastDue,
			refunds,
		)
	}
}

// TestFactsKeepTheSchemaAndDomainRules checks what the database and the
// recognition rules would otherwise reject or misread.
func TestFactsKeepTheSchemaAndDomainRules(t *testing.T) {
	pop := generate(20000, 0, 3, asOf)
	ids, codes, objects := map[[16]byte]bool{}, map[string]bool{}, map[string]bool{}
	unique := func(seen map[[16]byte]bool, id [16]byte) {
		if seen[id] {
			t.Fatalf("duplicate identifier %x", id)
		}
		seen[id] = true
	}
	for _, d := range pop.donors {
		unique(ids, d.id.Bytes)
		if d.code != "" {
			if codes[d.code] || len(d.code) != 32 || d.publicName == "" || d.claimedAt.IsZero() || !d.claimedAt.Before(d.sharingAt) {
				t.Fatalf("donor %d shares without a unique code after claiming", d.index)
			}
			codes[d.code] = true
		}
		monthly := 0
		for _, c := range d.checkouts {
			unique(ids, c.id.Bytes)
			if c.amount < 100 || (c.referrer == nil) != (c.kind == "") || c.referrer == d {
				t.Fatalf("checkout for donor %d breaks a checkout constraint", d.index)
			}
			if c.referrer != nil && !c.referrer.sharingAt.Before(c.expiresAt.Add(-referralWindow)) {
				t.Fatalf("donor %d was referred before the referrer had a code", d.index)
			}
			if c.frequency == "MONTHLY" && c.status != "EXPIRED" {
				monthly++
			}
			for i, p := range c.periods {
				if span := p.ends.Sub(p.due); span < 27*day || span > 32*day || p.due.After(pop.now) {
					t.Fatalf("period of %s for donor %d", span, d.index)
				}
				if i > 0 && p.due.Before(c.periods[i-1].ends) {
					t.Fatalf("overlapping periods for donor %d", d.index)
				}
				if p.paid == p.paidAt.IsZero() || (!p.paid && p.failedAt.IsZero()) {
					t.Fatalf("period recorded for donor %d without a reported outcome", d.index)
				}
			}
			for _, g := range c.gifts {
				if objects[g.object] || g.amount != c.amount || g.paidAt.After(pop.now) || g.paidAt.Before(c.createdAt) {
					t.Fatalf("gift for donor %d is not a valid collected payment", d.index)
				}
				objects[g.object] = true
				attributed := c.referrer != nil && g.paidAt.Before(c.expiresAt)
				if attributed != (g.referrer != nil) {
					t.Fatalf("gift for donor %d does not record attribution as of its charge", d.index)
				}
				if r := g.refund; r != nil {
					last := r.observations[len(r.observations)-1]
					if r.amount > g.amount || r.observations[0].at.Before(g.paidAt) || last.at.After(pop.now) || last.status != r.status {
						t.Fatalf("refund for donor %d is inconsistent", d.index)
					}
				}
			}
			if s := c.sub; s != nil && s.status == "canceled" && s.stopsAt.IsZero() {
				t.Fatalf("cancelled subscription for donor %d keeps renewing", d.index)
			}
			if p := c.pause; p != nil &&
				(p.months < 1 || p.months > 3 || p.resumes.Sub(p.starts) < time.Duration(p.months)*28*day || p.resumes.Sub(p.starts) > time.Duration(p.months)*31*day) {
				t.Fatalf("pause for donor %d skips the wrong renewals", d.index)
			}
		}
		if monthly > 1 || d.streak < 0 {
			t.Fatalf("donor %d has %d monthly subscriptions and streak %d", d.index, monthly, d.streak)
		}
	}
	for _, touch := range pop.touches {
		if touch.referrer.code == "" || touch.at.Before(touch.referrer.sharingAt) || touch.at.After(pop.now) {
			t.Fatal("a visit used a code that did not exist yet")
		}
	}
}

func TestAddMonthsKeepsTheAnniversaryOrMonthEnd(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 9, 30, 0, 0, time.UTC)
	for months, want := range map[int]string{1: "2026-02-28", 2: "2026-03-31", 3: "2026-04-30", 13: "2027-02-28", 25: "2028-02-29"} {
		if got := addMonths(anchor, months).Format(time.DateOnly); got != want {
			t.Errorf("%d months after %s: %s, want %s", months, anchor.Format(time.DateOnly), got, want)
		}
	}
}

func TestReferralCodesFollowInfluence(t *testing.T) {
	pop := generate(5000, 0, 1, asOf)
	codes := pop.referralCodes(fixtureCodes)
	visits := map[string]int{}
	for _, code := range codes {
		visits[code]++
	}
	var top *donor
	for _, d := range pop.donors {
		if d.code != "" && (top == nil || d.influence > top.influence) {
			top = d
		}
	}
	if len(codes) != fixtureCodes || visits[top.code] < 10 || len(visits) < 100 {
		t.Fatalf("codes do not follow influence: %d codes, %d distinct, top sharer %d", len(codes), len(visits), visits[top.code])
	}
}
