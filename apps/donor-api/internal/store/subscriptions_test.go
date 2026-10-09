package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
)

func paymentObject(t *testing.T, value map[string]any) payments.Object {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	out, err := payments.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func subscription(checkout string, start time.Time) map[string]any {
	return map[string]any{"id": "sub_" + checkout, "livemode": false, "status": "active", "customer": "cus_fixture", "cancel_at_period_end": false, "cancel_at": nil, "billing_cycle_anchor": start.Unix(), "metadata": map[string]any{"dad_checkout_id": checkout}, "items": map[string]any{"has_more": false, "data": []any{map[string]any{"id": "si_fixture", "quantity": 1, "current_period_start": start.Unix(), "current_period_end": start.Add(30 * 24 * time.Hour).Unix(), "price": map[string]any{"currency": "usd", "unit_amount": 3000, "product": "prod_fixture", "recurring": map[string]any{"interval": "month", "interval_count": 1}}}}}}
}
func subscriptionItem(remote map[string]any) map[string]any {
	return remote["items"].(map[string]any)["data"].([]any)[0].(map[string]any)
}
func assertEventProcessed(t *testing.T, f *workerFixture, id string, want bool) {
	t.Helper()
	var done bool
	if err := f.db.QueryRow(context.Background(), "SELECT processed_at IS NOT NULL FROM donor_stripe_events WHERE stripe_event_id=$1", id).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if done != want {
		t.Fatalf("processed=%v want=%v", done, want)
	}
}
func TestSubscriptionUsesCurrentStateAndReleasesCancelledReservation(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	remote := subscription(result.CheckoutID, start)
	remote["status"] = "canceled"
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	stale := subscription(result.CheckoutID, start)
	event := f.receive(t, "customer.subscription.updated", stale)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var status, attempt string
	var stops time.Time
	var reservations int
	if err := f.db.QueryRow(ctx, `SELECT s.status,s.stops_at,a.status FROM donor_subscriptions s JOIN donor_stripe_checkouts a ON a.id=s.attempt_id WHERE s.donor_id=$1`, f.donorID).Scan(&status, &stops, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "canceled" || attempt != "CANCELLED" || !stops.Equal(start.Add(30*24*time.Hour)) {
		t.Fatal("stale webhook overrode current cancellation")
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM donor_recurring_reservations WHERE donor_id=$1", f.donorID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatal("reservation not released", err)
	}
}
func TestCheckoutCompletionSyncsTheSubscriptionItBinds(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	var session pgtype.Text
	if err := f.db.QueryRow(ctx, "SELECT stripe_session_id FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if !session.Valid {
		session.String = "cs_test_" + result.CheckoutID
	}
	subID := "sub_" + result.CheckoutID
	completed := map[string]any{"id": session.String, "status": "complete", "subscription": subID, "metadata": map[string]any{"dad_checkout_id": result.CheckoutID}}
	event := f.receive(t, "checkout.session.completed", completed)
	f.process(t)
	assertEventProcessed(t, f, event, false)
	var bound bool
	if err := f.db.QueryRow(ctx, "SELECT stripe_subscription_id IS NOT NULL FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&bound); err != nil || bound {
		t.Fatal("completion applied without its subscription", err)
	}

	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	f.gateway.subscriptions[subID] = paymentObject(t, subscription(result.CheckoutID, start))
	f.retryNow(t)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var status, attempt string
	var periodEnd, anchor time.Time
	if err := f.db.QueryRow(ctx, `SELECT s.status,s.period_end,s.billing_anchor,a.status FROM donor_subscriptions s JOIN donor_stripe_checkouts a ON a.id=s.attempt_id WHERE a.checkout_id=$1`, result.CheckoutID).Scan(&status, &periodEnd, &anchor, &attempt); err != nil {
		t.Fatal("completion did not sync the subscription", err)
	}
	if status != "active" || attempt != "COMPLETE" || !periodEnd.Equal(start.Add(30*24*time.Hour)) || !anchor.Equal(start) {
		t.Fatalf("synced %s %s %s %s", status, attempt, periodEnd, anchor)
	}
}

func TestEverySubscriptionEventSyncsCurrentState(t *testing.T) {
	for _, kind := range []string{"customer.subscription.created", "customer.subscription.pending_update_applied", "customer.subscription.pending_update_expired"} {
		t.Run(kind, func(t *testing.T) {
			f := newWorkerFixture(t, "MONTHLY")
			result := f.checkout(t)
			start := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
			current := subscription(result.CheckoutID, start)
			current["cancel_at_period_end"] = true
			f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, current)
			event := f.receive(t, kind, subscription(result.CheckoutID, start))
			f.process(t)
			assertEventProcessed(t, f, event, true)
			var stops time.Time
			if err := f.db.QueryRow(context.Background(), "SELECT stops_at FROM donor_subscriptions WHERE donor_id=$1", f.donorID).Scan(&stops); err != nil || !stops.Equal(start.Add(30*24*time.Hour)) {
				t.Fatal("event did not sync the subscription's current state", stops, err)
			}
		})
	}
}
func TestInvalidSubscriptionRollsBackBindingAndProjection(t *testing.T) {
	cases := map[string]func(map[string]any){
		"live":        func(r map[string]any) { r["livemode"] = true },
		"owner":       func(r map[string]any) { r["metadata"] = map[string]any{"dad_checkout_id": "another"} },
		"extra items": func(r map[string]any) { r["items"].(map[string]any)["has_more"] = true },
		"quantity":    func(r map[string]any) { subscriptionItem(r)["quantity"] = 2 },
		"amount": func(r map[string]any) {
			subscriptionItem(r)["price"].(map[string]any)["unit_amount"] = 9000
		},
		"cancel midmonth": func(r map[string]any) { r["cancel_at"] = time.Now().Unix() },
		"pause without authorization": func(r map[string]any) {
			r["pause_collection"] = map[string]any{"behavior": "void", "resumes_at": time.Now().Add(90 * 24 * time.Hour).Unix()}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newWorkerFixture(t, "MONTHLY")
			result := f.checkout(t)
			remote := subscription(result.CheckoutID, time.Now().Add(-24*time.Hour))
			edit(remote)
			f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
			event := f.receive(t, "customer.subscription.updated", subscription(result.CheckoutID, time.Now()))
			f.process(t)
			assertEventProcessed(t, f, event, false)
			var bound bool
			var schedules int
			if err := f.db.QueryRow(context.Background(), "SELECT stripe_subscription_id IS NOT NULL FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&bound); err != nil || bound {
				t.Fatal("failed binding survived", err)
			}
			if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM donor_subscriptions WHERE donor_id=$1", f.donorID).Scan(&schedules); err != nil || schedules != 0 {
				t.Fatal("invalid schedule saved", err)
			}
		})
	}
}
func TestReconcileAuthorizedAmountPauseAndResumption(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	end := start.Add(30 * 24 * time.Hour)
	var attempt pgtype.UUID
	if err := f.db.QueryRow(ctx, "SELECT id FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	change, pause, resume := testID(t), testID(t), testID(t)
	if _, err := f.db.Exec(ctx, `INSERT INTO donor_amount_changes(id,donor_id,checkout_id,previous_minor,amount_minor,effective_at,item_id,product_id) VALUES($1,$2,$3,3000,9000,$4,'si_fixture','prod_fixture')`, change, f.donorID, result.CheckoutID, end); err != nil {
		t.Fatal(err)
	}
	until := end.Add(60 * 24 * time.Hour)
	if _, err := f.db.Exec(ctx, `INSERT INTO donor_pauses(id,donor_id,attempt_id,months,starts_at,resumes_at) VALUES($1,$2,$3,2,$4,$5)`, pause, f.donorID, attempt, end, until); err != nil {
		t.Fatal(err)
	}
	remote := subscription(result.CheckoutID, start)
	subscriptionItem(remote)["price"].(map[string]any)["unit_amount"] = 9000
	remote["pause_collection"] = map[string]any{"behavior": "void", "resumes_at": until.Unix()}
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	event := f.receive(t, "customer.subscription.updated", remote)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var confirmed bool
	for _, table := range []string{"donor_amount_changes", "donor_pauses"} {
		if err := f.db.QueryRow(ctx, "SELECT confirmed_at IS NOT NULL FROM "+table+" WHERE donor_id=$1", f.donorID).Scan(&confirmed); err != nil || !confirmed {
			t.Fatal("authorization not confirmed", table, err)
		}
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO donor_resumptions(id,donor_id,pause_id,next_renewal) VALUES($1,$2,$3,$4)`, resume, f.donorID, pause, end); err != nil {
		t.Fatal(err)
	}
	remote["pause_collection"] = map[string]any{"behavior": "void", "resumes_at": end.Unix()}
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	event = f.receive(t, "customer.subscription.updated", remote)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	if err := f.db.QueryRow(ctx, "SELECT confirmed_at IS NOT NULL FROM donor_resumptions WHERE id=$1", resume).Scan(&confirmed); err != nil || !confirmed {
		t.Fatal("resumption not confirmed", err)
	}
	remote["pause_collection"] = nil
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	event = f.receive(t, "customer.subscription.updated", remote)
	f.process(t)
	assertEventProcessed(t, f, event, false)
}

func TestInvoiceConfirmsPendingAmountWithoutRepricingPriorPeriod(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	end := start.Add(30 * 24 * time.Hour)
	if _, err := f.db.Exec(ctx, `INSERT INTO donor_amount_changes(id,donor_id,checkout_id,previous_minor,amount_minor,effective_at,item_id,product_id) VALUES($1,$2,$3,3000,9000,$4,'si_fixture','prod_fixture')`, testID(t), f.donorID, result.CheckoutID, end); err != nil {
		t.Fatal(err)
	}
	remote := subscription(result.CheckoutID, start)
	subscriptionItem(remote)["price"].(map[string]any)["unit_amount"] = 9000
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	original := invoice(result.CheckoutID, "in_old_"+result.CheckoutID, start, true)
	event := f.receive(t, "invoice.payment_succeeded", original)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	renewal := invoice(result.CheckoutID, "in_new_"+result.CheckoutID, end, true)
	renewal["amount_paid"] = 9000
	renewal["amount_due"] = 9000
	renewal["total"] = 9000
	renewal["lines"].(map[string]any)["data"].([]any)[0].(map[string]any)["amount"] = 9000
	renewal["payments"].(map[string]any)["data"].([]any)[0].(map[string]any)["amount_paid"] = 9000
	event = f.receive(t, "invoice.payment_succeeded", renewal)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var total int64
	if err := f.db.QueryRow(ctx, "SELECT sum(amount_minor) FROM donor_gifts WHERE donor_id=$1", f.donorID).Scan(&total); err != nil || total != 12000 {
		t.Fatal("invoice effective boundary changed", total, err)
	}
}

func TestExpiredCheckoutReleasesItsReservationButNeverUndoesACompletion(t *testing.T) {
	ctx := context.Background()
	reservations := func(f *workerFixture) int {
		var n int
		if err := f.db.QueryRow(ctx, "SELECT count(*) FROM donor_recurring_reservations WHERE donor_id=$1", f.donorID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	attemptStatus := func(f *workerFixture, checkout string) string {
		var status string
		if err := f.db.QueryRow(ctx, "SELECT status FROM donor_stripe_checkouts WHERE checkout_id=$1", checkout).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	sessionOf := func(f *workerFixture, checkout string) string {
		var session pgtype.Text
		if err := f.db.QueryRow(ctx, "SELECT stripe_session_id FROM donor_stripe_checkouts WHERE checkout_id=$1", checkout).Scan(&session); err != nil || !session.Valid {
			t.Fatal("checkout has no session", err)
		}
		return session.String
	}

	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	again := f.request
	again.RequestID = testID(t)
	if _, err := f.service.BeginCheckout(ctx, again); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatal("second monthly checkout allowed while the first is open", err)
	}
	foreign := f.receive(t, "checkout.session.expired", map[string]any{"id": "cs_someone_else", "status": "expired", "metadata": map[string]any{"dad_checkout_id": result.CheckoutID}})
	f.process(t)
	assertEventProcessed(t, f, foreign, false)
	if reservations(f) != 1 {
		t.Fatal("an expiry for another session released the reservation")
	}
	expired := f.receive(t, "checkout.session.expired", map[string]any{"id": sessionOf(f, result.CheckoutID), "status": "expired", "metadata": map[string]any{"dad_checkout_id": result.CheckoutID}})
	f.process(t)
	assertEventProcessed(t, f, expired, true)
	if attemptStatus(f, result.CheckoutID) != "EXPIRED" || reservations(f) != 0 {
		t.Fatal("expiry did not end the attempt and release its reservation")
	}
	if _, err := f.service.BeginCheckout(ctx, again); err != nil {
		t.Fatal("monthly checkout refused after the open one expired", err)
	}

	done := newWorkerFixture(t, "MONTHLY")
	finished := done.checkout(t)
	mustExec(t, done.db, "UPDATE donor_stripe_checkouts SET status='COMPLETE' WHERE checkout_id=$1", finished.CheckoutID)
	late := done.receive(t, "checkout.session.expired", map[string]any{"id": sessionOf(done, finished.CheckoutID), "status": "expired", "metadata": map[string]any{"dad_checkout_id": finished.CheckoutID}})
	done.process(t)
	assertEventProcessed(t, done, late, true)
	if attemptStatus(done, finished.CheckoutID) != "COMPLETE" || reservations(done) != 1 {
		t.Fatal("a late expiry undid a completed checkout")
	}
}
