package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"dad/donor-api/internal/store"
)

// table is one COPY target. Its rows are produced from the population as
// they are written, so no table is held in memory.
type table struct {
	name, columns string
	rows          func(emit func(...any))
}

func timeOrNull(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func textOrNull(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func donorID(d *donor) pgtype.UUID {
	if d == nil {
		return pgtype.UUID{}
	}
	return d.id
}

// derived identifies a row the model does not track by the row it belongs
// to. Only the last byte changes, keeping the UUID's version and variant.
func derived(id pgtype.UUID, tag byte) pgtype.UUID {
	id.Bytes[15] ^= tag
	return id
}

// event is a processor fact with the processed Stripe event it arrived in.
// Events are written only where an observation cites them, with no payload:
// the worker reads payloads of unprocessed events alone.
type event struct {
	id           pgtype.UUID
	kind, status string
	at           time.Time
}

func (pd *period) events() []event {
	var out []event
	if !pd.failedAt.IsZero() {
		out = append(out, event{derived(pd.id, 1), "invoice.payment_failed", "FAILED", pd.failedAt})
	}
	if pd.paid {
		out = append(out, event{derived(pd.id, 2), "invoice.payment_succeeded", "PAID", pd.paidAt})
	}
	return out
}

func (r *refund) events() []event {
	out := make([]event, len(r.observations))
	for i, o := range r.observations {
		out[i] = event{derived(r.id, byte(i+1)), "refund.updated", o.status, o.at}
	}
	out[0].kind = "refund.created"
	return out
}

func (p *population) checkouts(yield func(*checkout) bool) {
	for _, d := range p.donors {
		for _, c := range d.checkouts {
			if !yield(c) {
				return
			}
		}
	}
}

func (p *population) refunds(yield func(*gift) bool) {
	for c := range p.checkouts {
		for _, g := range c.gifts {
			if g.refund != nil && !yield(g) {
				return
			}
		}
	}
}

// tables lays the population out as the rows the checkout, webhook and
// worker paths store, in foreign-key order. A finished checkout attempt's
// request is never read again, so it is stored as an empty object with a
// unique hash.
func (p *population) tables() []table {
	return []table{
		{"donors", "id email claimed_at public_name referral_code created_at", func(emit func(...any)) {
			for _, d := range p.donors {
				emit(d.id, d.email, timeOrNull(d.claimedAt), textOrNull(d.publicName), textOrNull(d.code), d.createdAt)
			}
		}},
		{"donor_preferences", "id donor_id version referral_emails marketing_emails created_at", func(emit func(...any)) {
			for _, d := range p.donors {
				if d.preference {
					referral, marketing := d.emails()
					emit(derived(d.id, 1), d.id, int32(1), referral, marketing, d.claimedAt)
				}
			}
		}},
		{
			"donor_preference_events",
			"id donor_id version referral_emails marketing_emails consent_version source created_at",
			func(emit func(...any)) {
				for _, d := range p.donors {
					if d.preference {
						referral, marketing := d.emails()
						emit(derived(d.id, 2), d.id, int32(1), referral, marketing, "r1-notifications-v1", "account", d.claimedAt)
					}
				}
			},
		},
		{"donor_referral_touches", "id token_hash referrer_id kind touched_at expires_at created_at", func(emit func(...any)) {
			for _, t := range p.touches {
				emit(t.id, t.tokenHash, t.referrer.id, t.kind, t.at, t.at.Add(referralWindow), t.at)
			}
		}},
		{
			"donor_checkouts",
			"id donor_id amount_minor currency frequency referrer_id referral_kind referral_expires_at created_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					var expires pgtype.Timestamptz
					if c.referrer != nil {
						expires = timeOrNull(c.expiresAt)
					}
					emit(c.id, c.donor.id, c.amount, "USD", c.frequency, donorID(c.referrer), textOrNull(c.kind), expires, c.createdAt)
				}
			},
		},
		{
			"donor_stripe_checkouts",
			"id checkout_id request_hash request_params stripe_session_id stripe_subscription_id status created_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					emit(
						c.attempt,
						c.id,
						hex.EncodeToString(c.attempt.Bytes[:]),
						"{}",
						textOrNull(c.session),
						textOrNull(c.subscription),
						c.status,
						c.createdAt,
					)
				}
			},
		},
		{"donor_recurring_reservations", "id donor_id attempt_id created_at", func(emit func(...any)) {
			for c := range p.checkouts {
				if c.frequency == "MONTHLY" && c.status != "EXPIRED" && c.status != "CANCELLED" {
					emit(derived(c.attempt, 1), c.donor.id, c.attempt, c.createdAt)
				}
			}
		}},
		{
			"donor_subscriptions",
			"id attempt_id donor_id status period_end stops_at billing_anchor observed_at created_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					if s := c.sub; s != nil {
						emit(s.id, c.attempt, c.donor.id, s.status, s.periodEnd, timeOrNull(s.stopsAt), s.anchor, s.observed, c.createdAt)
					}
				}
			},
		},
		{"donor_cancellations", "id donor_id attempt_id confirmed_period_end completed_at created_at", func(emit func(...any)) {
			for c := range p.checkouts {
				if x := c.cancellation; x != nil {
					emit(x.id, c.donor.id, c.attempt, x.periodEnd, x.completed, x.created)
				}
			}
		}},
		{"donor_pauses", "id donor_id attempt_id months starts_at resumes_at confirmed_at created_at", func(emit func(...any)) {
			for c := range p.checkouts {
				if x := c.pause; x != nil {
					emit(x.id, c.donor.id, c.attempt, int32(x.months), x.starts, x.resumes, x.confirmed, x.created)
				}
			}
		}},
		{
			"donor_stripe_events",
			"id stripe_event_id event_type occurred_at data processed_at attempts available_at created_at",
			func(emit func(...any)) {
				write := func(e event) {
					emit(e.id, "evt_"+hex.EncodeToString(e.id.Bytes[:]), e.kind, e.at, "{}", e.at.Add(2*time.Second), int32(1), e.at, e.at)
				}
				for c := range p.checkouts {
					for _, pd := range c.periods {
						for _, e := range pd.events() {
							write(e)
						}
					}
				}
				for g := range p.refunds {
					for _, e := range g.refund.events() {
						write(e)
					}
				}
			},
		},
		{
			"donor_billing_periods",
			"id donor_id checkout_id stripe_invoice_id stripe_subscription_id due_at ends_at status created_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					for _, pd := range c.periods {
						status := "FAILED"
						if pd.paid {
							status = "PAID"
						}
						emit(pd.id, c.donor.id, c.id, pd.invoice, c.subscription, pd.due, pd.ends, status, pd.events()[0].at)
					}
				}
			},
		},
		{
			"donor_billing_observations",
			"id period_id source_event_id status amount_minor currency billing_reason occurred_at event_created_at recorded_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					for i, pd := range c.periods {
						reason := "subscription_cycle"
						if i == 0 {
							reason = "subscription_create"
						}
						for _, e := range pd.events() {
							emit(e.id, pd.id, e.id, e.status, c.amount, "USD", reason, e.at, e.at, e.at.Add(2*time.Second))
						}
					}
				}
			},
		},
		{
			"donor_gifts",
			"id donor_id checkout_id stripe_object_id amount_minor currency frequency paid_at stripe_payment_intent_id period_id referrer_id referral_kind created_at",
			func(emit func(...any)) {
				for c := range p.checkouts {
					for _, g := range c.gifts {
						var period pgtype.UUID
						if g.period != nil {
							period = g.period.id
						}
						emit(
							g.id,
							c.donor.id,
							c.id,
							g.object,
							g.amount,
							"USD",
							g.frequency,
							g.paidAt,
							g.paymentIntent,
							period,
							donorID(g.referrer),
							textOrNull(g.kind),
							g.paidAt.Add(2*time.Second),
						)
					}
				}
			},
		},
		{"donor_refunds", "id gift_id stripe_refund_id amount_minor currency status observed_at created_at", func(emit func(...any)) {
			for g := range p.refunds {
				r := g.refund
				emit(r.id, g.id, r.stripeID, r.amount, "USD", r.status, r.observations[len(r.observations)-1].at, r.observations[0].at)
			}
		}},
		{"donor_refund_observations", "id refund_id source_event_id status observed_at created_at", func(emit func(...any)) {
			for g := range p.refunds {
				for _, e := range g.refund.events() {
					emit(e.id, g.refund.id, e.id, e.status, e.at, e.at.Add(2*time.Second))
				}
			}
		}},
		{"donor_metric_projections", "id donor_id computed_at next_refresh_at", func(emit func(...any)) {
			for _, d := range p.donors {
				emit(derived(d.id, 3), d.id, p.now, p.now)
			}
		}},
	}
}

// copyAll writes every table and the seed marker in one transaction, so a
// failed copy leaves nothing behind.
func copyAll(ctx context.Context, pool *pgxpool.Pool, tables []table) (map[string]int64, error) {
	counts := map[string]int64{}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		name := pgx.Identifier{pool.Config().ConnConfig.Database}.Sanitize()
		if _, err := tx.Exec(ctx, "COMMENT ON DATABASE "+name+" IS '"+seedMarker+"'"); err != nil {
			return fmt.Errorf("mark database: %w", err)
		}
		for _, t := range tables {
			// A COPY that fails stops pulling; the rows still produced go nowhere.
			next, stop := iter.Pull(func(yield func([]any) bool) { t.rows(func(row ...any) { yield(row) }) })
			n, err := tx.CopyFrom(ctx, pgx.Identifier{t.name}, strings.Fields(t.columns), pgx.CopyFromFunc(func() ([]any, error) {
				row, _ := next()
				return row, nil
			}))
			stop()
			if err != nil {
				return fmt.Errorf("copy %s: %w", t.name, err)
			}
			counts[t.name] = n
		}
		return nil
	})
	return counts, err
}

// refreshProjections derives every due projection with the projection
// worker's own code, so streaks, totals and awards are computed exactly as
// they are for real donors.
func refreshProjections(ctx context.Context, pool *pgxpool.Pool, workers int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var once sync.Once
	var failure error
	for range workers {
		wg.Go(func() {
			for {
				worked, err := store.RefreshNextProjection(ctx, pool, time.Now().UTC())
				if err != nil {
					once.Do(func() { failure = err; cancel() })
				}
				if err != nil || !worked {
					return
				}
			}
		})
	}
	if wg.Wait(); failure != nil {
		return failure
	}
	var parked int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_metric_projections WHERE refresh_error IS NOT NULL").Scan(&parked); err != nil {
		return err
	}
	if parked > 0 {
		return fmt.Errorf("%d seeded projections could not be computed", parked)
	}
	return nil
}

// suppressHistory marks the messages the projections queued for seeded
// history as handled, so a mail worker never sends receipts for synthetic
// gifts. Real donors received these when their gifts were collected.
func suppressHistory(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `UPDATE donor_messages m SET completed_at=now(),suppressed_reason='synthetic load-test history'
FROM donors d WHERE d.id=m.donor_id AND d.email LIKE '%@`+emailDomain+`' AND m.completed_at IS NULL`)
	return tag.RowsAffected(), err
}

// fixture is what the load generator needs: donor sessions and referral codes
// weighted by how often each code would be visited.
type fixture struct {
	Sessions      []string `json:"sessions"`
	ReferralCodes []string `json:"referral_codes"`
}

// sessions signs in n claimed donors, favouring active referrers, whose
// dashboards are the most expensive and the most visited. Tokens come from
// the system random source like real sessions; only their hashes are stored.
type session struct {
	donor *donor
	token string
}

func (p *population) sessions(ctx context.Context, pool *pgxpool.Pool, n int) ([]session, error) {
	type candidate struct {
		d   *donor
		key float64
	}
	var candidates []candidate
	for _, d := range p.donors {
		if d.claimedAt.IsZero() {
			continue
		}
		weight := 1 + float64(d.referrals())
		// Weighted sampling without replacement (Efraimidis–Spirakis), keyed
		// by the donor's random ID so the chosen donors are reproducible.
		u := (float64(uint32(d.id.Bytes[0])<<16|uint32(d.id.Bytes[1])<<8|uint32(d.id.Bytes[2])) + 0.5) / (1 << 24)
		candidates = append(candidates, candidate{d, math.Pow(u, 1/weight)})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int { return cmp.Compare(b.key, a.key) })
	candidates = candidates[:min(n, len(candidates))]
	sessions := make([]session, len(candidates))
	rows := make([][]any, len(candidates))
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	for i, c := range candidates {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		sessions[i] = session{c.d, base64.RawURLEncoding.EncodeToString(secret)}
		id, err := store.NewID()
		if err != nil {
			return nil, err
		}
		rows[i] = []any{id, c.d.id, store.HashToken(sessions[i].token), expires}
	}
	_, err := pool.CopyFrom(
		ctx,
		pgx.Identifier{"donor_sessions"},
		[]string{"id", "donor_id", "token_hash", "expires_at"},
		pgx.CopyFromRows(rows),
	)
	return sessions, err
}

// writeFixture signs in n donors and writes their tokens, with referral
// codes, to w, returning how many sessions it created.
func (p *population) writeFixture(ctx context.Context, pool *pgxpool.Pool, n int, w io.Writer) (int, error) {
	sessions, err := p.sessions(ctx, pool, n)
	if err != nil {
		return 0, err
	}
	f := fixture{Sessions: make([]string, len(sessions)), ReferralCodes: p.referralCodes(fixtureCodes)}
	for i, s := range sessions {
		f.Sessions[i] = s.token
	}
	return len(sessions), json.NewEncoder(w).Encode(f)
}

// referralCodes draws n codes in proportion to sharer influence, so popular
// codes recur in the load mix as they would in a viral spike.
func (p *population) referralCodes(n int) []string {
	var sharers []*donor
	var totals []float64
	total := 0.0
	for _, d := range p.donors {
		if d.code != "" {
			total += d.influence
			sharers, totals = append(sharers, d), append(totals, total)
		}
	}
	if len(sharers) == 0 {
		return []string{}
	}
	codes := make([]string, n)
	for i := range codes {
		x := total * (float64(i) + 0.5) / float64(n)
		j, _ := slices.BinarySearch(totals, x)
		codes[i] = sharers[min(j, len(sharers)-1)].code
	}
	return codes
}
