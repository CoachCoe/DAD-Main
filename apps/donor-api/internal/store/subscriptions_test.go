package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
)

func paymentObject(t *testing.T, value map[string]interface{}) payments.Object {
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
func subscription(checkout string, start time.Time) map[string]interface{} {
	return map[string]interface{}{"id": "sub_" + checkout, "livemode": false, "status": "active", "customer": "cus_fixture", "cancel_at_period_end": false, "cancel_at": nil, "metadata": map[string]interface{}{"dad_checkout_id": checkout}, "items": map[string]interface{}{"has_more": false, "data": []interface{}{map[string]interface{}{"id": "si_fixture", "quantity": 1, "current_period_start": start.Unix(), "current_period_end": start.Add(30 * 24 * time.Hour).Unix(), "price": map[string]interface{}{"currency": "usd", "unit_amount": 3000, "product": "prod_fixture", "recurring": map[string]interface{}{"interval": "month", "interval_count": 1}}}}}}
}
func subscriptionItem(remote map[string]interface{}) map[string]interface{} {
	return remote["items"].(map[string]interface{})["data"].([]interface{})[0].(map[string]interface{})
}
func assertEventProcessed(t *testing.T, f *workerFixture, id string, want bool) {
	t.Helper()
	var done bool
	if err := f.service.DB.QueryRow(context.Background(), "SELECT processed_at IS NOT NULL FROM donor_stripe_events WHERE stripe_event_id=$1", id).Scan(&done); err != nil {
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
	if err := f.service.DB.QueryRow(ctx, `SELECT s.status,s.stops_at,a.status FROM donor_subscriptions s JOIN donor_stripe_checkouts a ON a.id=s.attempt_id WHERE s.donor_id=$1`, f.donorID).Scan(&status, &stops, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "canceled" || attempt != "CANCELLED" || !stops.Equal(start.Add(30*24*time.Hour)) {
		t.Fatal("stale webhook overrode current cancellation")
	}
	if err := f.service.DB.QueryRow(ctx, "SELECT count(*) FROM donor_recurring_reservations WHERE donor_id=$1", f.donorID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatal("reservation not released", err)
	}
}
func TestInvalidSubscriptionRollsBackBindingAndProjection(t *testing.T) {
	cases := map[string]func(map[string]interface{}){
		"live":        func(r map[string]interface{}) { r["livemode"] = true },
		"owner":       func(r map[string]interface{}) { r["metadata"] = map[string]interface{}{"dad_checkout_id": "another"} },
		"extra items": func(r map[string]interface{}) { r["items"].(map[string]interface{})["has_more"] = true },
		"quantity":    func(r map[string]interface{}) { subscriptionItem(r)["quantity"] = 2 },
		"amount": func(r map[string]interface{}) {
			subscriptionItem(r)["price"].(map[string]interface{})["unit_amount"] = 9000
		},
		"cancel midmonth": func(r map[string]interface{}) { r["cancel_at"] = time.Now().Unix() },
		"pause without authorization": func(r map[string]interface{}) {
			r["pause_collection"] = map[string]interface{}{"behavior": "void", "resumes_at": time.Now().Add(90 * 24 * time.Hour).Unix()}
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
			if err := f.service.DB.QueryRow(context.Background(), "SELECT stripe_subscription_id IS NOT NULL FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&bound); err != nil || bound {
				t.Fatal("failed binding survived", err)
			}
			if err := f.service.DB.QueryRow(context.Background(), "SELECT count(*) FROM donor_subscriptions WHERE donor_id=$1", f.donorID).Scan(&schedules); err != nil || schedules != 0 {
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
	if err := f.service.DB.QueryRow(ctx, "SELECT id FROM donor_stripe_checkouts WHERE checkout_id=$1", result.CheckoutID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	change, pause, resume := testID(t), testID(t), testID(t)
	if _, err := f.service.DB.Exec(ctx, `INSERT INTO donor_amount_changes(id,donor_id,checkout_id,previous_minor,amount_minor,effective_at,item_id,product_id) VALUES($1,$2,$3,3000,9000,$4,'si_fixture','prod_fixture')`, change, f.donorID, result.CheckoutID, end); err != nil {
		t.Fatal(err)
	}
	until := end.Add(60 * 24 * time.Hour)
	if _, err := f.service.DB.Exec(ctx, `INSERT INTO donor_pauses(id,donor_id,attempt_id,months,starts_at,resumes_at) VALUES($1,$2,$3,2,$4,$5)`, pause, f.donorID, attempt, end, until); err != nil {
		t.Fatal(err)
	}
	remote := subscription(result.CheckoutID, start)
	subscriptionItem(remote)["price"].(map[string]interface{})["unit_amount"] = 9000
	remote["pause_collection"] = map[string]interface{}{"behavior": "void", "resumes_at": until.Unix()}
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	event := f.receive(t, "customer.subscription.updated", remote)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var confirmed bool
	for _, table := range []string{"donor_amount_changes", "donor_pauses"} {
		if err := f.service.DB.QueryRow(ctx, "SELECT confirmed_at IS NOT NULL FROM "+table+" WHERE donor_id=$1", f.donorID).Scan(&confirmed); err != nil || !confirmed {
			t.Fatal("authorization not confirmed", table, err)
		}
	}
	if _, err := f.service.DB.Exec(ctx, `INSERT INTO donor_resumptions(id,donor_id,pause_id,next_renewal) VALUES($1,$2,$3,$4)`, resume, f.donorID, pause, end); err != nil {
		t.Fatal(err)
	}
	remote["pause_collection"] = map[string]interface{}{"behavior": "void", "resumes_at": end.Unix()}
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	event = f.receive(t, "customer.subscription.updated", remote)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	if err := f.service.DB.QueryRow(ctx, "SELECT confirmed_at IS NOT NULL FROM donor_resumptions WHERE id=$1", resume).Scan(&confirmed); err != nil || !confirmed {
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
	if _, err := f.service.DB.Exec(ctx, `INSERT INTO donor_amount_changes(id,donor_id,checkout_id,previous_minor,amount_minor,effective_at,item_id,product_id) VALUES($1,$2,$3,3000,9000,$4,'si_fixture','prod_fixture')`, testID(t), f.donorID, result.CheckoutID, end); err != nil {
		t.Fatal(err)
	}
	remote := subscription(result.CheckoutID, start)
	subscriptionItem(remote)["price"].(map[string]interface{})["unit_amount"] = 9000
	f.gateway.subscriptions["sub_"+result.CheckoutID] = paymentObject(t, remote)
	original := invoice(result.CheckoutID, "in_old_"+result.CheckoutID, start, true)
	event := f.receive(t, "invoice.payment_succeeded", original)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	renewal := invoice(result.CheckoutID, "in_new_"+result.CheckoutID, end, true)
	renewal["amount_paid"] = 9000
	renewal["amount_due"] = 9000
	renewal["total"] = 9000
	renewal["lines"].(map[string]interface{})["data"].([]interface{})[0].(map[string]interface{})["amount"] = 9000
	renewal["payments"].(map[string]interface{})["data"].([]interface{})[0].(map[string]interface{})["amount_paid"] = 9000
	event = f.receive(t, "invoice.payment_succeeded", renewal)
	f.process(t)
	assertEventProcessed(t, f, event, true)
	var total int64
	if err := f.service.DB.QueryRow(ctx, "SELECT sum(amount_minor) FROM donor_gifts WHERE donor_id=$1", f.donorID).Scan(&total); err != nil || total != 12000 {
		t.Fatal("invoice effective boundary changed", total, err)
	}
}
