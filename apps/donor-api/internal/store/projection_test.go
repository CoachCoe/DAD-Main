package store

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/recognition"
	"impactgraph/donor-api/internal/store/queries"
)

type referralScenario struct {
	Name     string               `json:"name"`
	Op       string               `json:"operation"`
	AsOf     time.Time            `json:"as_of"`
	Gifts    []recognition.Gift   `json:"gifts"`
	Refunds  []recognition.Refund `json:"refunds"`
	Expected json.RawMessage      `json:"expected"`
}

type referralTotals struct {
	Link      int64 `json:"link_count"`
	IRL       int64 `json:"irl_count"`
	LinkMinor int64 `json:"link_minor"`
	IRLMinor  int64 `json:"irl_minor"`
}

// historyFixture writes scenario facts through the production gift, refund and
// projection paths inside a transaction the test rolls back.
type historyFixture struct {
	t       *testing.T
	tx      pgx.Tx
	q       *queries.Queries
	donors  map[string]pgtype.UUID
	objects map[string]string
}

func newHistoryFixture(t *testing.T, tx pgx.Tx) *historyFixture {
	return &historyFixture{t: t, tx: tx, q: queries.New(tx), donors: map[string]pgtype.UUID{}, objects: map[string]string{}}
}
func (h *historyFixture) donor(name string) pgtype.UUID {
	h.t.Helper()
	if id, ok := h.donors[name]; ok {
		return id
	}
	id := testID(h.t)
	if err := h.q.InsertGuest(context.Background(), queries.InsertGuestParams{ID: id, Email: id.String() + "@example.org"}); err != nil {
		h.t.Fatal(err)
	}
	h.donors[name] = id
	return id
}
func (h *historyFixture) gift(g recognition.Gift) {
	h.t.Helper()
	ctx := context.Background()
	if g.Frequency != "ONE_TIME" {
		h.t.Fatal("history fixture supports one-time gifts")
	}
	donor := h.donor(g.DonorID)
	draft := queries.InsertCheckoutParams{ID: testID(h.t), DonorID: donor, AmountMinor: g.Amount, Frequency: g.Frequency, CreatedAt: timestamp(g.PaidAt)}
	// Checkout never captures a donor's own referral touch, so the schema forbids it.
	if c := g.Candidate; c != nil && c.ReferrerID != g.DonorID {
		draft.ReferrerID = h.donor(c.ReferrerID)
		draft.ReferralKind = pgtype.Text{String: c.Kind, Valid: true}
		draft.ReferralExpiresAt = timestamp(c.ExpiresAt)
	}
	if err := h.q.InsertCheckout(ctx, draft); err != nil {
		h.t.Fatal(err)
	}
	checkout, err := h.q.CheckoutDraft(ctx, draft.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	object := "pi_" + testID(h.t).String()
	h.objects[g.ID] = object
	if err = recordGift(ctx, h.q, checkout, paidGift{objectID: object, amount: g.Amount, paid: g.PaidAt, paymentIntent: object}, g.PaidAt); err != nil {
		h.t.Fatal(err)
	}
}
func (h *historyFixture) refund(r recognition.Refund) {
	h.t.Helper()
	ctx := context.Background()
	gift, err := h.q.GiftByStripeObject(ctx, h.objects[r.GiftID])
	if err != nil {
		h.t.Fatal(err)
	}
	event := testID(h.t)
	if _, err = h.tx.Exec(ctx, `INSERT INTO donor_stripe_events (id,stripe_event_id,event_type,occurred_at,data) VALUES ($1,$2,'refund.updated',$3,'{}')`, event, "evt_"+event.String(), r.UpdatedAt); err != nil {
		h.t.Fatal(err)
	}
	current := paymentObject(h.t, map[string]any{"id": "re_" + event.String(), "payment_intent": gift.StripePaymentIntentID.String, "amount": r.Amount, "currency": "usd", "status": strings.ToLower(r.Status)})
	if err = recordRefund(ctx, h.q, gift, current, event, pgtype.UUID{}, r.UpdatedAt); err != nil {
		h.t.Fatal(err)
	}
}

func TestSharedReferralScenariosAgainstSQLAggregate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/recognition.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []referralScenario
	if err = json.Unmarshal(data, &scenarios); err != nil {
		t.Fatal(err)
	}
	pool := database(t)
	ran := 0
	for _, c := range scenarios {
		if c.Op != "referrals" {
			continue
		}
		ran++
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var expected map[string]referralTotals
			if err = json.Unmarshal(c.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			h := newHistoryFixture(t, tx)
			for _, g := range c.Gifts {
				h.gift(g)
			}
			for _, r := range c.Refunds {
				h.refund(r)
			}
			names := make([]string, 0, len(h.donors))
			for name := range h.donors {
				names = append(names, name)
			}
			slices.Sort(names)
			actAs(t, tx, dbroles.ProjectionWorker)
			for _, name := range names {
				if _, err = refreshProjection(ctx, tx, h.donors[name], c.AsOf); err != nil {
					t.Fatal(name, err)
				}
			}
			actAs(t, tx, dbroles.API)
			for _, name := range names {
				want := expected[name]
				board, err := dashboard(ctx, h.q, h.donors[name], c.AsOf)
				if err != nil {
					t.Fatal(err)
				}
				referrals := board.Referrals
				if referrals == nil {
					t.Fatalf("%s: referral totals not ready: %+v", name, board)
				}
				got := referralTotals{Link: referrals.Link, IRL: referrals.IRL, LinkMinor: referrals.LinkMinor, IRLMinor: referrals.IRLMinor}
				if got != want {
					t.Fatalf("%s: got %+v want %+v", name, got, want)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("missing referral scenarios")
	}
}

func TestGiftStoresAttributionValidWhenCharged(t *testing.T) {
	pool := database(t)
	referrer, _ := donor(t, pool)
	accountCleanup(t, pool, referrer)
	f := newWorkerFixture(t, "ONE_TIME")
	ctx := context.Background()
	visitor := "visitor-" + f.donorID.String()
	f.request.Visitor = &visitor
	if _, err := pool.Exec(ctx, `INSERT INTO donor_referral_touches (id,token_hash,referrer_id,kind,touched_at,expires_at) VALUES ($1,$2,$3,'LINK',$4,$5)`, testID(t), HashToken(visitor), referrer, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DELETE FROM donor_referral_touches WHERE referrer_id=$1", referrer); err != nil {
			t.Error(err)
		}
	})
	pay := func(checkout, payment string) {
		f.charge(t, payment, time.Now())
		f.receive(t, "payment_intent.succeeded", map[string]any{"id": payment, "metadata": map[string]any{"dad_checkout_id": checkout}, "status": "succeeded", "currency": "usd", "amount_received": 3000})
		f.process(t)
	}
	first := f.checkout(t)
	pay(first.CheckoutID, "pi_first_"+f.donorID.String())
	var touchReferrer, giftReferrer, checkoutReferrer pgtype.UUID
	var touchKind, giftKind, checkoutKind string
	if err := pool.QueryRow(ctx, `SELECT t.referrer_id,t.kind,c.referrer_id,c.referral_kind,g.referrer_id,g.referral_kind
FROM donor_gifts g JOIN donor_checkouts c ON c.id=g.checkout_id JOIN donor_referral_touches t ON t.token_hash=$2
WHERE g.stripe_object_id=$1`, "pi_first_"+f.donorID.String(), HashToken(visitor)).Scan(&touchReferrer, &touchKind, &checkoutReferrer, &checkoutKind, &giftReferrer, &giftKind); err != nil {
		t.Fatal(err)
	}
	if giftReferrer != referrer || checkoutReferrer != referrer || touchReferrer != referrer || giftKind != "LINK" || checkoutKind != touchKind || giftKind != touchKind {
		t.Fatal("payment record lost first-touch attribution")
	}
	// A charge after the attribution window keeps the checkout's snapshot but is not referred.
	f.request.RequestID = testID(t)
	second := f.checkout(t)
	if _, err := pool.Exec(ctx, "UPDATE donor_checkouts SET referral_expires_at=now()-interval '1 second' WHERE id=$1", second.CheckoutID); err != nil {
		t.Fatal(err)
	}
	pay(second.CheckoutID, "pi_second_"+f.donorID.String())
	var attributed bool
	if err := pool.QueryRow(ctx, "SELECT referrer_id IS NOT NULL OR referral_kind IS NOT NULL FROM donor_gifts WHERE stripe_object_id=$1", "pi_second_"+f.donorID.String()).Scan(&attributed); err != nil || attributed {
		t.Fatal("expired attribution written to payment", err)
	}
	p, err := queries.New(pool).Projection(ctx, f.donorID)
	if err != nil || p.ReferrerID != referrer || p.ReferralKind.String != "LINK" {
		t.Fatal("projection disagrees with payment attribution", err)
	}
}

func TestConversionAndFoundingArePermanentOnceProjected(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	h := newHistoryFixture(t, tx)
	later := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	earlier := later.AddDate(0, -1, 0)
	window := later.AddDate(0, 1, 0)
	h.gift(recognition.Gift{ID: "later", DonorID: "ben", Amount: 3000, Currency: "USD", Frequency: "ONE_TIME", PaidAt: later, Candidate: &recognition.Candidate{ReferrerID: "alice", Kind: "LINK", ExpiresAt: window}})
	first, err := projectFacts(ctx, queries.New(tx), h.donor("ben"), later)
	if err != nil || first.ReferrerID != h.donor("alice") || !first.FoundingAwardedAt.Time.Equal(later) {
		t.Fatalf("first projection: %+v %v", first, err)
	}
	// An earlier gift processed late must not move the converted relationship.
	h.gift(recognition.Gift{ID: "earlier", DonorID: "ben", Amount: 3000, Currency: "USD", Frequency: "ONE_TIME", PaidAt: earlier, Candidate: &recognition.Candidate{ReferrerID: "cara", Kind: "IRL", ExpiresAt: window}})
	second, err := projectFacts(ctx, queries.New(tx), h.donor("ben"), later.Add(time.Hour))
	if err != nil || second.ReferrerID != first.ReferrerID || second.ReferralKind != first.ReferralKind || second.FirstGiftID != first.FirstGiftID || !second.FoundingAwardedAt.Time.Equal(first.FoundingAwardedAt.Time) || second.NetMinor.Int64 != 6000 {
		t.Fatalf("relationship moved: %+v %v", second, err)
	}
}

func TestRefundRecordedByLaterClockUsesEarlierObservation(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	h := newHistoryFixture(t, tx)
	paid := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := paid.Add(time.Hour)
	h.gift(recognition.Gift{ID: "g", DonorID: "ben", Amount: 3000, Currency: "USD", Frequency: "ONE_TIME", PaidAt: paid})
	h.refund(recognition.Refund{GiftID: "g", Amount: 3000, Status: "PENDING", UpdatedAt: asOf.Add(-time.Minute)})
	ahead := asOf.Add(2 * time.Second)
	if _, err = tx.Exec(ctx, "UPDATE donor_refunds SET status='SUCCEEDED',observed_at=$2 WHERE gift_id=(SELECT id FROM donor_gifts WHERE stripe_object_id=$1)", h.objects["g"], ahead); err != nil {
		t.Fatal(err)
	}
	before, err := projectFacts(ctx, queries.New(tx), h.donor("ben"), asOf)
	if err != nil || before.NetMinor.Int64 != 3000 || !before.NextRefreshAt.Time.Equal(ahead) {
		t.Fatalf("as-of refund state: %+v %v", before, err)
	}
	after, err := projectFacts(ctx, queries.New(tx), h.donor("ben"), ahead)
	if err != nil || after.NetMinor.Int64 != 0 {
		t.Fatalf("later refund state: %+v %v", after, err)
	}
}

func TestRefreshNextProjectionDefersUnprojectableDonor(t *testing.T) {
	pool := database(t)
	worker := as(t, dbroles.ProjectionWorker)
	ctx := context.Background()
	// A historical clock keeps other tests' due projections out of this queue.
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	poisoned, healthy := testID(t), testID(t)
	checkout, gift := testID(t), testID(t)
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_messages WHERE donor_id=ANY($1)",
			"DELETE FROM donor_refunds WHERE gift_id IN (SELECT id FROM donor_gifts WHERE donor_id=ANY($1))",
			"DELETE FROM donor_gifts WHERE donor_id=ANY($1)",
			"DELETE FROM donor_checkouts WHERE donor_id=ANY($1)",
			"DELETE FROM donor_metric_projections WHERE donor_id=ANY($1)",
			"DELETE FROM donors WHERE id=ANY($1)",
		} {
			if _, err := pool.Exec(context.Background(), sql, []pgtype.UUID{poisoned, healthy}); err != nil {
				t.Error(err)
			}
		}
	})
	for i, id := range []pgtype.UUID{poisoned, healthy} {
		if _, err := pool.Exec(ctx, "INSERT INTO donors(id,email,created_at) VALUES($1,$2,$3)", id, id.String()+"@example.org", now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO donor_metric_projections(id,donor_id,computed_at,next_refresh_at) VALUES($1,$2,$3,$4)", testID(t), id, now, now.Add(time.Duration(i-2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// A succeeded refund larger than its gift cannot be projected.
	if _, err := pool.Exec(ctx, "INSERT INTO donor_checkouts(id,donor_id,amount_minor,currency,frequency) VALUES($1,$2,3000,'USD','ONE_TIME')", checkout, poisoned); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO donor_gifts(id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at) VALUES($1,$2,$3,$4,3000,'USD','ONE_TIME',$5)", gift, poisoned, checkout, "pi_"+gift.String(), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO donor_refunds(id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) VALUES($1,$2,$3,9000,'USD','SUCCEEDED',$4)", testID(t), gift, "re_"+gift.String(), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if worked, err := RefreshNextProjection(ctx, worker, now); err != nil || !worked {
			t.Fatal("projection worker stalled", worked, err)
		}
	}
	if worked, err := RefreshNextProjection(ctx, worker, now); err != nil || worked {
		t.Fatal("deferred donor retried immediately", worked, err)
	}
	q := queries.New(pool)
	bad, err := q.Projection(ctx, poisoned)
	if err != nil || bad.RefreshFailures != 1 || bad.RefreshError.String != "InvalidFacts" || !bad.NextRefreshAt.Time.Equal(now.Add(5*time.Minute)) || bad.NetMinor.Valid {
		t.Fatalf("poisoned projection: %+v %v", bad, err)
	}
	good, err := q.Projection(ctx, healthy)
	if err != nil || !good.NetMinor.Valid || !good.ComputedAt.Time.Equal(now) || good.RefreshError.Valid {
		t.Fatalf("healthy projection blocked: %+v %v", good, err)
	}
	rows, err := queries.New(as(t, dbroles.Monitor)).QueueHealth(ctx, timestamp(now.Add(5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Queue == "projections" && (row.Due != 1 || row.Parked != 1) {
			t.Fatal("unprojectable donor not surfaced", row)
		}
	}
	if worked, err := RefreshNextProjection(ctx, worker, now.Add(5*time.Minute)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if bad, err = q.Projection(ctx, poisoned); err != nil || bad.RefreshFailures != 2 || !bad.NextRefreshAt.Time.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("backoff did not grow: %+v %v", bad, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE donor_refunds SET amount_minor=1000 WHERE gift_id=$1", gift); err != nil {
		t.Fatal(err)
	}
	if worked, err := RefreshNextProjection(ctx, worker, now.Add(15*time.Minute)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if bad, err = q.Projection(ctx, poisoned); err != nil || bad.RefreshFailures != 0 || bad.RefreshError.Valid || bad.NetMinor.Int64 != 2000 {
		t.Fatalf("corrected donor not recovered: %+v %v", bad, err)
	}
}
