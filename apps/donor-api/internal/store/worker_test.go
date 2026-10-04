package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v87"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type financialGateway struct {
	refunds             map[string]payments.Object
	subscriptions       map[string]payments.Object
	sessions            map[string]payments.Object
	setups              map[string]payments.Object
	updateKeys          []string
	loseAcknowledgement bool
}

func (g *financialGateway) RetrieveRefund(_ context.Context, id string) (payments.Object, error) {
	value, ok := g.refunds[id]
	if !ok {
		return nil, payments.ErrCheckoutUnavailable
	}
	return value, nil
}
func (g *financialGateway) RetrieveSubscription(_ context.Context, id string) (payments.Object, error) {
	value, ok := g.subscriptions[id]
	if !ok {
		return nil, payments.ErrCheckoutUnavailable
	}
	return value, nil
}
func (g *financialGateway) RetrieveCheckout(_ context.Context, id string) (payments.Object, error) {
	value, ok := g.sessions[id]
	if !ok {
		return nil, payments.ErrCheckoutUnavailable
	}
	return value, nil
}
func (g *financialGateway) RetrieveSetupIntent(_ context.Context, id string) (payments.Object, error) {
	value, ok := g.setups[id]
	if !ok {
		return nil, payments.ErrCheckoutUnavailable
	}
	return value, nil
}
func (g *financialGateway) UpdateSubscriptionPaymentMethod(_ context.Context, id, method, key string) error {
	g.updateKeys = append(g.updateKeys, key)
	value, ok := g.subscriptions[id]
	if !ok {
		return payments.ErrCheckoutUnavailable
	}
	value["default_payment_method"], _ = json.Marshal(method)
	if g.loseAcknowledgement {
		g.loseAcknowledgement = false
		return payments.ErrCheckoutUnavailable
	}
	return nil
}

type workerFixture struct {
	service CheckoutService
	request payments.CheckoutRequest
	donorID pgtype.UUID
	worker  EventWorker
	events  []string
	gateway *financialGateway
}

func newWorkerFixture(t *testing.T, frequency string) *workerFixture {
	t.Helper()
	service, request, id, _ := checkoutFixture(t)
	request.Frequency = frequency
	gateway := &financialGateway{refunds: map[string]payments.Object{}, subscriptions: map[string]payments.Object{}, sessions: map[string]payments.Object{}, setups: map[string]payments.Object{}}
	f := &workerFixture{service: service, request: request, donorID: id, worker: EventWorker{DB: service.DB, Gateway: gateway}, gateway: gateway}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			"DELETE FROM donor_messages WHERE donor_id=$1 OR dedup_key=$2",
			"DELETE FROM donor_refund_observations WHERE refund_id IN (SELECT r.id FROM donor_refunds r JOIN donor_gifts g ON g.id=r.gift_id WHERE g.donor_id=$1)",
			"DELETE FROM donor_refunds WHERE gift_id IN (SELECT id FROM donor_gifts WHERE donor_id=$1)",
			"DELETE FROM donor_gifts WHERE donor_id=$1",
			"DELETE FROM donor_billing_periods WHERE donor_id=$1",
			"DELETE FROM donor_metric_projections WHERE donor_id=$1",
			"DELETE FROM donor_login_emails WHERE donor_id=$1",
			"DELETE FROM donor_subscriptions WHERE donor_id=$1",
			"DELETE FROM donor_resumptions WHERE donor_id=$1",
			"DELETE FROM donor_pauses WHERE donor_id=$1",
			"DELETE FROM donor_amount_changes WHERE donor_id=$1",
			"DELETE FROM donor_payment_updates WHERE donor_id=$1",
		} {
			args := []interface{}{id}
			if sql == "DELETE FROM donor_messages WHERE donor_id=$1 OR dedup_key=$2" {
				args = append(args, "referral:"+id.String())
			}
			if _, err := service.DB.Exec(ctx, sql, args...); err != nil {
				t.Error(err)
			}
		}
		if _, err := service.DB.Exec(ctx, "DELETE FROM donor_stripe_events WHERE stripe_event_id=ANY($1::text[])", f.events); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *workerFixture) checkout(t *testing.T) payments.CheckoutResult {
	t.Helper()
	result, err := f.service.BeginCheckout(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *workerFixture) receive(t *testing.T, kind string, obj map[string]interface{}) string {
	t.Helper()
	eventID := "evt_" + f.request.RequestID.String() + "_" + strconv.Itoa(len(f.events))
	f.events = append(f.events, eventID)
	now := time.Now()
	body, err := json.Marshal(map[string]interface{}{"id": eventID, "object": "event", "type": kind, "api_version": payments.APIVersion, "created": now.Unix(), "livemode": false, "data": map[string]interface{}{"object": obj}})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "whsec_public_worker_test"
	signature := fmt.Sprintf("t=%d,v1=%x", now.Unix(), stripe.ComputeSignature(now, body, secret))
	receiver := payments.Receiver{Secret: secret, Inbox: StripeInbox{DB: f.service.DB}}
	if err = receiver.Receive(context.Background(), body, signature); err != nil {
		t.Fatal(err)
	}
	return eventID
}
func (f *workerFixture) process(t *testing.T) {
	t.Helper()
	worked, err := f.worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("worker did not handle event: %v", err)
	}
}
func invoice(checkout, id string, start time.Time, paid bool) map[string]interface{} {
	status, amountPaid := "open", 0
	var paidAt interface{}
	if paid {
		status = "paid"
		amountPaid = 3000
		paidAt = start.Add(time.Hour).Unix()
	}
	return map[string]interface{}{"id": id, "status": status, "currency": "usd", "amount_paid": amountPaid, "amount_due": 3000, "total": 3000, "starting_balance": 0, "ending_balance": 0, "billing_reason": "subscription_cycle",
		"parent":             map[string]interface{}{"subscription_details": map[string]interface{}{"subscription": "sub_" + checkout, "metadata": map[string]interface{}{"dad_checkout_id": checkout}}},
		"status_transitions": map[string]interface{}{"paid_at": paidAt},
		"lines":              map[string]interface{}{"has_more": false, "data": []interface{}{map[string]interface{}{"amount": 3000, "currency": "usd", "period": map[string]interface{}{"start": start.Unix(), "end": start.Add(30 * 24 * time.Hour).Unix()}, "parent": map[string]interface{}{"type": "subscription_item_details", "subscription_item_details": map[string]interface{}{"subscription": "sub_" + checkout, "proration": false}}}}},
		"payments":           map[string]interface{}{"has_more": false, "data": []interface{}{map[string]interface{}{"status": "paid", "amount_paid": 3000, "currency": "usd", "invoice": id, "livemode": false, "payment": map[string]interface{}{"type": "payment_intent", "payment_intent": "pi_" + id}}}},
	}
}
func TestGuestInvoiceClaimDashboardAndRefund(t *testing.T) {
	pool := database(t)
	referrer, _ := donor(t, pool)
	accountCleanup(t, pool, referrer)
	f := newWorkerFixture(t, "MONTHLY")
	ctx := context.Background()
	visitor := "visitor-" + f.donorID.String()
	f.request.Visitor = &visitor
	if _, err := f.service.DB.Exec(ctx, `INSERT INTO donor_referral_touches (id,token_hash,referrer_id,kind,touched_at,expires_at) VALUES ($1,$2,$3,'IRL',$4,$5)`, testID(t), HashToken(visitor), referrer, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DELETE FROM donor_referral_touches WHERE referrer_id=$1", referrer); err != nil {
			t.Error(err)
		}
	})
	checkout := f.checkout(t)
	invoiceID := "in_" + f.donorID.String()
	start := time.Now().UTC().Truncate(time.Second).Add(-2 * 24 * time.Hour)
	obj := invoice(checkout.CheckoutID, invoiceID, start, true)
	eventID := f.receive(t, "invoice.payment_succeeded", obj)
	f.process(t)
	var processed *time.Time
	if err := pool.QueryRow(ctx, "SELECT processed_at FROM donor_stripe_events WHERE stripe_event_id=$1", eventID).Scan(&processed); err != nil || processed == nil {
		t.Fatal("valid gift not committed")
	}
	f.receive(t, "invoice.payment_succeeded", obj)
	f.process(t)
	// A delayed failure must not undo a confirmed period.
	f.receive(t, "invoice.payment_failed", invoice(checkout.CheckoutID, invoiceID, start, false))
	f.process(t)
	var gifts, receipts, emails, recovery int
	for _, tc := range []struct {
		sql    string
		target *int
	}{
		{"SELECT count(*) FROM donor_gifts WHERE donor_id=$1", &gifts}, {"SELECT count(*) FROM donor_messages WHERE donor_id=$1 AND kind='RECEIPT'", &receipts},
		{"SELECT count(*) FROM donor_login_emails WHERE donor_id=$1", &emails}, {"SELECT count(*) FROM donor_messages WHERE donor_id=$1 AND kind='RECOVERY'", &recovery},
	} {
		if err := pool.QueryRow(ctx, tc.sql, f.donorID).Scan(tc.target); err != nil {
			t.Fatal(err)
		}
	}
	if gifts != 1 || receipts != 1 || emails != 1 || recovery != 0 {
		t.Fatalf("duplicate or out-of-order side effects: %d %d %d %d", gifts, receipts, emails, recovery)
	}
	token, err := (AccountService{DB: pool}).ClaimAccount(ctx, link(t, pool, f.donorID, time.Now()), "")
	if err != nil {
		t.Fatal(err)
	}
	dashboard, err := (DashboardReader{DB: pool}).DonorDashboard(ctx, token)
	if err != nil || dashboard["founding_donor"] != true || dashboard["streak"].(map[string]interface{})["current"] != int32(1) {
		t.Fatalf("claimed dashboard: %v %v", dashboard, err)
	}
	refToken, err := (AccountService{DB: pool}).ClaimAccount(ctx, link(t, pool, referrer, time.Now()), "")
	if err != nil {
		t.Fatal(err)
	}
	refDashboard, err := (DashboardReader{DB: pool}).DonorDashboard(ctx, refToken)
	if err != nil {
		t.Fatal(err)
	}
	attribution := refDashboard["referrals"].(map[string]interface{})
	if attribution["irl"] != int64(1) || attribution["total_minor"] != int64(3000) {
		t.Fatal("guest referral not credited")
	}
	refundID := "re_" + f.donorID.String()
	raw, err := json.Marshal(map[string]interface{}{"id": refundID, "payment_intent": "pi_" + invoiceID, "amount": 3000, "currency": "usd", "status": "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	f.gateway.refunds[refundID], err = payments.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	f.receive(t, "refund.updated", map[string]interface{}{"id": refundID, "payment_intent": "pi_" + invoiceID, "status": "pending"})
	f.process(t)
	projection, err := queries.New(pool).Projection(ctx, f.donorID)
	if err != nil || projection.NetMinor.Int64 != 0 || projection.CurrentStreak.Int32 != 0 || projection.BestStreak != 1 || !projection.FoundingAwardedAt.Valid {
		t.Fatalf("refund recognition: %+v %v", projection, err)
	}
	refDashboard, err = (DashboardReader{DB: pool}).DonorDashboard(ctx, refToken)
	if err != nil {
		t.Fatal(err)
	}
	attribution = refDashboard["referrals"].(map[string]interface{})
	if attribution["irl"] != int64(0) || attribution["total_minor"] != int64(0) {
		t.Fatal("refund retained collected dollars")
	}
}
func TestInvoiceFailureRecoveryPreservesGrace(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	checkout := f.checkout(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-32 * 24 * time.Hour)
	f.receive(t, "invoice.payment_succeeded", invoice(checkout.CheckoutID, "in_first_"+f.donorID.String(), start, true))
	f.process(t)
	second := "in_second_" + f.donorID.String()
	due := start.Add(30 * 24 * time.Hour)
	f.receive(t, "invoice.payment_failed", invoice(checkout.CheckoutID, second, due, false))
	f.process(t)
	p, err := queries.New(f.service.DB).Projection(ctx, f.donorID)
	if err != nil || !p.CurrentStreak.Valid || p.CurrentStreak.Int32 != 1 || !p.AtRisk || !p.GraceEndsAt.Time.Equal(due.Add(14*24*time.Hour)) {
		t.Fatalf("grace lost: %+v %v", p, err)
	}
	f.receive(t, "invoice.payment_succeeded", invoice(checkout.CheckoutID, second, due, true))
	f.process(t)
	p, err = queries.New(f.service.DB).Projection(ctx, f.donorID)
	if err != nil || p.CurrentStreak.Int32 != 2 || p.AtRisk || p.GraceEndsAt.Valid {
		t.Fatal("recovery did not restore streak")
	}
}
func TestInvalidInvoiceRollsBackAllFinancialEffects(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	checkout := f.checkout(t)
	ctx := context.Background()
	obj := invoice(checkout.CheckoutID, "in_bad_"+f.donorID.String(), time.Now().Add(-24*time.Hour), true)
	obj["amount_paid"], obj["amount_due"], obj["total"] = 9000, 9000, 9000 // Fails after recording the valid period inside the savepoint.
	obj["payments"].(map[string]interface{})["data"].([]interface{})[0].(map[string]interface{})["amount_paid"] = 9000
	eventID := f.receive(t, "invoice.payment_succeeded", obj)
	for i := 0; i < 10; i++ {
		f.process(t)
	}
	if worked, err := f.worker.ProcessNext(ctx); err != nil || worked {
		t.Fatal("exhausted event did not park")
	}
	for _, table := range []string{"donor_gifts", "donor_billing_periods", "donor_metric_projections", "donor_login_emails", "donor_messages"} {
		var count int
		if err := f.service.DB.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE donor_id=$1", f.donorID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial financial write survived: %s %d %v", table, count, err)
		}
	}
	var attempts int
	var processed *time.Time
	var failure string
	if err := f.service.DB.QueryRow(ctx, "SELECT attempts,processed_at,last_error FROM donor_stripe_events WHERE stripe_event_id=$1", eventID).Scan(&attempts, &processed, &failure); err != nil || attempts != 10 || processed != nil || failure != "ValueError" {
		t.Fatal("failure budget not durable")
	}
}
func TestOneTimeInboxConcurrentWorkers(t *testing.T) {
	f := newWorkerFixture(t, "ONE_TIME")
	checkout := f.checkout(t)
	ctx := context.Background()
	f.receive(t, "payment_intent.succeeded", map[string]interface{}{"id": "pi_" + f.donorID.String(), "metadata": map[string]interface{}{"dad_checkout_id": checkout.CheckoutID}, "status": "succeeded", "currency": "usd", "amount_received": 3000})
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); worked, err := f.worker.ProcessNext(ctx); results <- worked; failures <- err }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	processed := 0
	for value := range results {
		if value {
			processed++
		}
	}
	if processed != 1 {
		t.Fatal("event claimed by multiple workers")
	}
	p, err := queries.New(f.service.DB).Projection(ctx, f.donorID)
	if err != nil || p.NetMinor.Int64 != 3000 || p.CurrentStreak.Int32 != 0 {
		t.Fatal("one-time payment changed streak")
	}
	if _, err = Authenticated(ctx, queries.New(f.service.DB), "", time.Now()); !errors.Is(err, ErrDenied) {
		t.Fatal("payment granted account access")
	}
}
