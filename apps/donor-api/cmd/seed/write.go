package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/store"
)

// table is one COPY target: its columns and rows in that order.
type table struct {
	name    string
	columns []string
	rows    [][]any
}

func (t *table) add(values ...any) { t.rows = append(t.rows, values) }

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

// tables lays the population out as the rows the checkout, webhook and
// worker paths store, in foreign-key order. Each processor fact carries the
// processed Stripe event it would have arrived in, holding the allowlisted
// fields the webhook keeps.
func (p *population) tables() ([]*table, error) {
	donors := &table{name: "donors", columns: []string{"id", "email", "claimed_at", "public_name", "referral_code", "created_at"}}
	preferences := &table{name: "donor_preferences", columns: []string{"id", "donor_id", "version", "referral_emails", "marketing_emails", "created_at"}}
	consents := &table{name: "donor_preference_events", columns: []string{"id", "donor_id", "version", "referral_emails", "marketing_emails", "consent_version", "source", "created_at"}}
	touches := &table{name: "donor_referral_touches", columns: []string{"id", "token_hash", "referrer_id", "kind", "touched_at", "expires_at", "created_at"}}
	checkouts := &table{name: "donor_checkouts", columns: []string{"id", "donor_id", "amount_minor", "currency", "frequency", "referrer_id", "referral_kind", "referral_expires_at", "created_at"}}
	attempts := &table{name: "donor_stripe_checkouts", columns: []string{"id", "checkout_id", "request_hash", "request_params", "stripe_session_id", "stripe_subscription_id", "status", "created_at"}}
	reservations := &table{name: "donor_recurring_reservations", columns: []string{"id", "donor_id", "attempt_id", "created_at"}}
	subscriptions := &table{name: "donor_subscriptions", columns: []string{"id", "attempt_id", "donor_id", "status", "period_end", "stops_at", "billing_anchor", "observed_at", "created_at"}}
	cancellations := &table{name: "donor_cancellations", columns: []string{"id", "donor_id", "attempt_id", "confirmed_period_end", "completed_at", "created_at"}}
	pauses := &table{name: "donor_pauses", columns: []string{"id", "donor_id", "attempt_id", "months", "starts_at", "resumes_at", "confirmed_at", "created_at"}}
	events := &table{name: "donor_stripe_events", columns: []string{"id", "stripe_event_id", "event_type", "occurred_at", "data", "processed_at", "attempts", "available_at", "created_at"}}
	periods := &table{name: "donor_billing_periods", columns: []string{"id", "donor_id", "checkout_id", "stripe_invoice_id", "stripe_subscription_id", "due_at", "ends_at", "status", "created_at"}}
	observations := &table{name: "donor_billing_observations", columns: []string{"id", "period_id", "source_event_id", "status", "amount_minor", "currency", "billing_reason", "occurred_at", "event_created_at", "recorded_at"}}
	gifts := &table{name: "donor_gifts", columns: []string{"id", "donor_id", "checkout_id", "stripe_object_id", "amount_minor", "currency", "frequency", "paid_at", "stripe_payment_intent_id", "period_id", "referrer_id", "referral_kind", "created_at"}}
	refunds := &table{name: "donor_refunds", columns: []string{"id", "gift_id", "stripe_refund_id", "amount_minor", "currency", "status", "observed_at", "created_at"}}
	refundObservations := &table{name: "donor_refund_observations", columns: []string{"id", "refund_id", "source_event_id", "status", "observed_at", "created_at"}}
	projections := &table{name: "donor_metric_projections", columns: []string{"id", "donor_id", "computed_at", "next_refresh_at"}}

	ids := &generator{r: p.ids}
	event := func(kind string, at time.Time, data map[string]any) (pgtype.UUID, error) {
		encoded, err := json.Marshal(data)
		if err != nil {
			return pgtype.UUID{}, err
		}
		id := ids.uuid()
		events.add(id, "evt_"+fmt.Sprintf("%x", id.Bytes[:12]), kind, at, encoded, at.Add(2*time.Second), int32(1), at, at)
		return id, nil
	}
	for _, d := range p.donors {
		donors.add(d.id, d.email, timeOrNull(d.claimedAt), textOrNull(d.publicName), textOrNull(d.code), d.createdAt)
		projections.add(ids.uuid(), d.id, p.now, p.now)
		if d.preference {
			referral, marketing := d.index%3 != 0, d.index%4 == 0
			preferences.add(ids.uuid(), d.id, int32(1), referral, marketing, d.claimedAt)
			consents.add(ids.uuid(), d.id, int32(1), referral, marketing, "r1-notifications-v1", "account", d.claimedAt)
		}
		for _, c := range d.checkouts {
			var expires pgtype.Timestamptz
			if c.referrer != nil {
				expires = timeOrNull(c.expiresAt)
			}
			checkouts.add(c.id, d.id, c.amount, "USD", c.frequency, donorID(c.referrer), textOrNull(c.kind), expires, c.createdAt)
			hash, params, err := checkoutRequest(d, c)
			if err != nil {
				return nil, err
			}
			attempts.add(c.attempt, c.id, hash, params, textOrNull(c.session), textOrNull(c.subscription), c.status, c.createdAt)
			if c.frequency == "MONTHLY" && c.status != "EXPIRED" && c.status != "CANCELLED" {
				reservations.add(ids.uuid(), d.id, c.attempt, c.createdAt)
			}
			if s := c.sub; s != nil {
				subscriptions.add(s.id, c.attempt, d.id, s.status, s.periodEnd, timeOrNull(s.stopsAt), s.anchor, s.observed, c.createdAt)
			}
			if x := c.cancellation; x != nil {
				cancellations.add(x.id, d.id, c.attempt, x.periodEnd, x.completed, x.created)
			}
			if x := c.pause; x != nil {
				pauses.add(x.id, d.id, c.attempt, int32(x.months), x.starts, x.resumes, x.confirmed, x.created)
			}
			for _, pd := range c.periods {
				status := "FAILED"
				if pd.paid {
					status = "PAID"
				}
				periods.add(pd.id, d.id, c.id, pd.invoice, c.subscription, pd.due, pd.ends, status, pd.recordedAt)
				reason := "subscription_cycle"
				if pd == c.periods[0] {
					reason = "subscription_create"
				}
				if !pd.failedAt.IsZero() {
					id, err := event("invoice.payment_failed", pd.failedAt, invoiceData(c, pd, reason, "open"))
					if err != nil {
						return nil, err
					}
					observations.add(ids.uuid(), pd.id, id, "FAILED", c.amount, "USD", reason, pd.failedAt, pd.failedAt, pd.failedAt.Add(2*time.Second))
				}
				if pd.paid {
					id, err := event("invoice.payment_succeeded", pd.paidAt, invoiceData(c, pd, reason, "paid"))
					if err != nil {
						return nil, err
					}
					observations.add(ids.uuid(), pd.id, id, "PAID", c.amount, "USD", reason, pd.paidAt, pd.paidAt, pd.paidAt.Add(2*time.Second))
				}
			}
			for _, g := range c.gifts {
				var period pgtype.UUID
				if g.period != nil {
					period = g.period.id
				} else if _, err := event("payment_intent.succeeded", g.paidAt, map[string]any{"id": g.object, "checkout_id": c.id.String(), "subscription": nil,
					"status": "succeeded", "currency": "usd", "amount_received": g.amount}); err != nil {
					return nil, err
				}
				gifts.add(g.id, d.id, c.id, g.object, g.amount, "USD", g.frequency, g.paidAt, g.paymentIntent, period, donorID(g.referrer), textOrNull(g.kind), g.paidAt.Add(2*time.Second))
				r := g.refund
				if r == nil {
					continue
				}
				last := r.observations[len(r.observations)-1]
				refunds.add(r.id, g.id, r.stripeID, r.amount, "USD", r.status, last.at, r.observations[0].at)
				for i, o := range r.observations {
					kind := "refund.created"
					if i > 0 {
						kind = "refund.updated"
					}
					id, err := event(kind, o.at, map[string]any{"id": r.stripeID, "payment_intent": g.paymentIntent, "amount": r.amount, "currency": "usd", "status": strings.ToLower(o.status)})
					if err != nil {
						return nil, err
					}
					refundObservations.add(ids.uuid(), r.id, id, o.status, o.at, o.at.Add(2*time.Second))
				}
			}
		}
	}
	for _, t := range p.touches {
		touches.add(t.id, t.tokenHash, t.referrer.id, t.kind, t.at, t.at.Add(referralWindow), t.at)
	}
	return []*table{donors, preferences, consents, touches, checkouts, attempts, reservations, subscriptions, cancellations,
		pauses, events, periods, observations, gifts, refunds, refundObservations, projections}, nil
}

// invoiceData is the minimal invoice the webhook would have stored.
func invoiceData(c *checkout, p *period, reason, status string) map[string]any {
	data := map[string]any{"id": p.invoice, "checkout_id": c.id.String(), "subscription": c.subscription, "status": status, "currency": "usd",
		"amount_due": c.amount, "total": c.amount, "billing_reason": reason, "starting_balance": 0, "ending_balance": 0,
		"lines_complete": true, "lines": []any{map[string]any{"amount": c.amount, "currency": "usd", "period": map[string]any{"start": p.due.Unix(), "end": p.ends.Unix()},
			"parent_type": "subscription_item_details", "subscription": c.subscription, "proration": false}},
		"payments_complete": true, "payments": []any{}, "paid_at": nil}
	if status == "paid" {
		data["amount_paid"] = c.amount
		data["paid_at"] = p.paidAt.Unix()
		for _, g := range c.gifts {
			if g.period == p {
				data["payments"] = []any{map[string]any{"status": "paid", "amount_paid": c.amount, "currency": "usd", "invoice": p.invoice,
					"livemode": false, "type": "payment_intent", "payment_intent": g.paymentIntent}}
			}
		}
	}
	return data
}

// checkoutRequest is the attempt's frozen request hash and hosted checkout
// parameters, in the shape checkout preparation records.
func checkoutRequest(d *donor, c *checkout) (string, []byte, error) {
	var visitor *string
	if c.referrer != nil {
		token := c.id.String()
		visitor = &token
	}
	preimage, err := credentials.CheckoutPreimage(d.email, c.amount, c.frequency, visitor)
	if err != nil {
		return "", nil, err
	}
	metadata := map[string]string{"dad_checkout_id": c.id.String()}
	if c.referrer != nil {
		metadata["dad_referrer_id"], metadata["dad_referral_kind"] = c.referrer.id.String(), c.kind
	}
	mode, name := "payment", "DAD one-time gift"
	price := map[string]any{"currency": "usd", "unit_amount": c.amount, "product_data": map[string]string{"name": name}}
	params := map[string]any{"mode": mode, "customer_email": d.email, "client_reference_id": c.id.String(), "metadata": metadata,
		"line_items": []any{map[string]any{"price_data": price, "quantity": 1}}, "allowed_payment_method_types": []string{"card"}}
	if c.frequency == "MONTHLY" {
		params["mode"] = "subscription"
		price["product_data"] = map[string]string{"name": "DAD monthly giving"}
		price["recurring"] = map[string]string{"interval": "month"}
		params["subscription_data"] = map[string]any{"metadata": metadata}
	} else {
		params["payment_intent_data"] = map[string]any{"metadata": metadata}
	}
	encoded, err := json.Marshal(params)
	return store.HashToken(preimage), encoded, err
}

// copyAll writes every table inside one transaction, so a failed seed
// leaves no partial population behind.
func copyAll(ctx context.Context, pool *pgxpool.Pool, tables []*table) (map[string]int64, error) {
	counts := map[string]int64{}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, t := range tables {
			n, err := tx.CopyFrom(ctx, pgx.Identifier{t.name}, t.columns, pgx.CopyFromRows(t.rows))
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
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"donor_sessions"}, []string{"id", "donor_id", "token_hash", "expires_at"}, pgx.CopyFromRows(rows))
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
